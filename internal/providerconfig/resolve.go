package providerconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/modelcatalog"
)

func loadFile(path string) (Resolved, error) {
	config, err := decodeConfigFile(path)
	if err != nil {
		return Resolved{}, err
	}
	if err := validateModelsMap(config); err != nil {
		return Resolved{}, err
	}
	providerID, modelID, ok := strings.Cut(strings.TrimSpace(config.Model), "/")
	providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	if !ok || providerID == "" || modelID == "" {
		return Resolved{}, errors.New("provider config model must use provider/model format")
	}
	return resolveFromFile(path, config, providerID, modelID)
}

// validateModelsMap checks optional top-level models role map (ADR-045).
func validateModelsMap(config File) error {
	if len(config.Models) == 0 {
		return nil
	}
	for role, ref := range config.Models {
		role = strings.TrimSpace(role)
		if role == "" {
			return errors.New("models map has empty role key")
		}
		if role == RoleMain {
			return errors.New(`models.main is not allowed; use top-level "model"`)
		}
		if _, ok := AllowedModelRoles[role]; !ok {
			return fmt.Errorf("models.%s is not a supported role (allowed: %s)", role, allowedModelRoleList())
		}
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		providerID, modelID, ok := strings.Cut(ref, "/")
		providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
		if !ok || providerID == "" || modelID == "" {
			return fmt.Errorf("models.%s must use provider/model format", role)
		}
		provider, exists := config.Provider[providerID]
		if !exists {
			return fmt.Errorf("models.%s provider %q is not configured", role, providerID)
		}
		if len(provider.Models) > 0 {
			if _, ok := lookupModel(provider.Models, providerID, modelID); !ok {
				return fmt.Errorf("models.%s: %w", role, errModelNotInCatalog(providerID, modelID, provider.Models))
			}
		}
	}
	return nil
}

func loadFileWithModel(path, providerID, modelID string) (Resolved, error) {
	config, err := decodeConfigFile(path)
	if err != nil {
		return Resolved{}, err
	}
	if err := validateModelsMap(config); err != nil {
		return Resolved{}, err
	}
	return resolveFromFile(path, config, providerID, modelID)
}

func validateModelInFile(path, providerID, modelID string) error {
	config, err := decodeConfigFile(path)
	if err != nil {
		return err
	}
	provider, ok := config.Provider[providerID]
	if !ok {
		return fmt.Errorf("selected provider %q is not configured", providerID)
	}
	if len(provider.Models) > 0 {
		if _, ok := lookupModel(provider.Models, providerID, modelID); !ok {
			return errModelNotInCatalog(providerID, modelID, provider.Models)
		}
	}
	return nil
}

// lookupModel finds a model entry under provider.models (OpenCode-style).
// Selection modelID is everything after the first '/' in "provider/model…"; catalog
// keys match that segment exactly and may themselves contain '/'.
// Also accept a mistaken key "providerID/modelID" when selection modelID is bare.
// Empty catalog: ok with zero Model (pass-through wire id = modelID).
func lookupModel(catalog map[string]Model, providerID, modelID string) (Model, bool) {
	if len(catalog) == 0 {
		return Model{}, true
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return Model{}, false
	}
	if m, ok := catalog[modelID]; ok {
		return m, true
	}
	// Mistaken catalog key: "provider/model" while selection model segment is bare.
	providerID = strings.TrimSpace(providerID)
	if providerID != "" {
		if m, ok := catalog[providerID+"/"+modelID]; ok {
			return m, true
		}
	}
	return Model{}, false
}

func errModelNotInCatalog(providerID, modelID string, catalog map[string]Model) error {
	keys := make([]string, 0, len(catalog))
	for k := range catalog {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	hint := `; catalog key must equal the model segment of selection (after first "/"; may contain "/") or set models.<key>.id for wire override`
	if providerID != "" && modelID != "" {
		for _, k := range keys {
			if k == providerID+"/"+modelID {
				hint = fmt.Sprintf(`; catalog key %q matches provider/model — use bare or nested model segment %q as the key`, k, modelID)
				break
			}
		}
	}
	return fmt.Errorf("model %q is not in provider %q catalog (keys: %s)%s", modelID, providerID, strings.Join(keys, ", "), hint)
}

func decodeConfigFile(path string) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return File{}, fmt.Errorf("open provider config %s: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var config File
	if err := decoder.Decode(&config); err != nil {
		return File{}, fmt.Errorf("decode provider config %s: %w", path, err)
	}
	if err := requireEOF(decoder); err != nil {
		return File{}, fmt.Errorf("decode provider config %s: %w", path, err)
	}
	return config, nil
}

func resolveFromFile(path string, config File, providerID, modelID string) (Resolved, error) {
	if providerID == "" || modelID == "" {
		return Resolved{}, errors.New("provider config model must use provider/model format")
	}
	provider, ok := config.Provider[providerID]
	if !ok {
		return Resolved{}, fmt.Errorf("selected provider %q is not configured", providerID)
	}
	protocol, err := resolveProtocol(provider.Protocol, provider.Type)
	if err != nil {
		return Resolved{}, fmt.Errorf("provider %q: %w", providerID, err)
	}
	// OpenCode: selection = providerID + "/" + modelID; modelID may contain '/'.
	// Wire id = models.<key>.id if set, else modelID (never strip provider prefix).
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	modelConfig, modelConfigured := lookupModel(provider.Models, providerID, modelID)
	if len(provider.Models) > 0 && !modelConfigured {
		return Resolved{}, errModelNotInCatalog(providerID, modelID, provider.Models)
	}
	wireID := strings.TrimSpace(modelConfig.ID)
	if wireID == "" {
		wireID = modelID
	}
	baseURL := strings.TrimSpace(provider.Options.BaseURL)
	if baseURL == "" {
		return Resolved{}, fmt.Errorf("provider %q baseURL is required", providerID)
	}
	apiKey, err := resolveValue(provider.Options.APIKey, filepath.Dir(path))
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve provider %q apiKey: %w", providerID, err)
	}
	responseFormat := strings.TrimSpace(modelConfig.ResponseFormat)
	if responseFormat == "" {
		responseFormat = strings.TrimSpace(provider.Options.ResponseFormat)
	}
	if protocol != ProtocolOpenAIChat && responseFormat != "" {
		return Resolved{}, fmt.Errorf("provider %q responseFormat is only supported by openai-chat", providerID)
	}
	if modelConfig.Temperature != nil && (*modelConfig.Temperature < 0 || *modelConfig.Temperature > 2) {
		return Resolved{}, fmt.Errorf("model %q temperature must be between 0 and 2", modelID)
	}
	if err := applyModelLimits(&modelConfig, modelID); err != nil {
		return Resolved{}, err
	}
	reasoningEffort := strings.TrimSpace(modelConfig.ReasoningEffort)
	if reasoningEffort != "" && protocol != ProtocolOpenAIChat && protocol != ProtocolOpenAIResponses {
		return Resolved{}, fmt.Errorf("model %q reasoningEffort is only supported by OpenAI-compatible protocols", modelID)
	}
	headers, err := resolveValues(provider.Options.Headers, filepath.Dir(path))
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve provider %q headers: %w", providerID, err)
	}
	return Resolved{
		Source: path, SelectionRef: providerID + "/" + modelID,
		ProviderID: providerID, Protocol: protocol, ModelID: wireID,
		BaseURL: baseURL, APIKey: apiKey,
		CompletionPath:  strings.TrimSpace(provider.Options.CompletionPath),
		ModelsPath:      strings.TrimSpace(provider.Options.ModelsPath),
		ResponseFormat:  responseFormat,
		Temperature:     modelConfig.Temperature,
		MaxTokens:       modelConfig.MaxTokens,
		ContextWindow:   modelConfig.ContextWindow,
		ReasoningEffort: reasoningEffort,

		AnthropicVersion: strings.TrimSpace(provider.Options.AnthropicVersion),
		Headers:          headers,
	}, nil
}

func applyModelLimits(modelConfig *Model, modelID string) error {
	if modelConfig.MaxTokens < 0 {
		return fmt.Errorf("model %q maxTokens must not be negative", modelID)
	}
	if modelConfig.ContextWindow < 0 {
		return fmt.Errorf("model %q contextWindow must not be negative", modelID)
	}
	if modelConfig.Limit != nil {
		if modelConfig.Limit.Context < 0 {
			return fmt.Errorf("model %q limit.context must not be negative", modelID)
		}
		if modelConfig.Limit.Output < 0 {
			return fmt.Errorf("model %q limit.output must not be negative", modelID)
		}
		if modelConfig.ContextWindow == 0 && modelConfig.Limit.Context > 0 {
			modelConfig.ContextWindow = modelConfig.Limit.Context
		}
		if modelConfig.MaxTokens == 0 && modelConfig.Limit.Output > 0 {
			modelConfig.MaxTokens = modelConfig.Limit.Output
		}
	}
	if modelConfig.ContextWindow > 0 {
		return nil
	}
	query := strings.TrimSpace(modelConfig.ID)
	if query == "" {
		query = modelID
	}
	modelConfig.ContextWindow = modelcatalog.Lookup(query).Context
	return nil
}

func resolveProtocol(protocol, providerType string) (string, error) {
	protocol = strings.TrimSpace(protocol)
	providerType = strings.TrimSpace(providerType)
	if protocol == "" && providerType == "" {
		return ProtocolOpenAIChat, nil
	}
	resolvedProtocol, err := normalizeProtocol(protocol)
	if err != nil && protocol != "" {
		return "", err
	}
	resolvedType, err := normalizeProtocol(providerType)
	if err != nil && providerType != "" {
		return "", fmt.Errorf("unsupported type %q", providerType)
	}
	if resolvedProtocol != "" && resolvedType != "" && resolvedProtocol != resolvedType {
		return "", fmt.Errorf("protocol %q conflicts with type %q", protocol, providerType)
	}
	if resolvedProtocol != "" {
		return resolvedProtocol, nil
	}
	return resolvedType, nil
}

func normalizeProtocol(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case ProtocolOpenAIChat, "openai-chat-completions", "chat-completions", "openai-compatible", "openai-compat", "ollama", "lmstudio", "llamacpp", "vllm", "litellm":
		return ProtocolOpenAIChat, nil
	case ProtocolOpenAIResponses, "openai", "responses":
		return ProtocolOpenAIResponses, nil
	case ProtocolAnthropicMessages, "anthropic", "anthropic-compatible", "claude":
		return ProtocolAnthropicMessages, nil
	case ProtocolGeminiGenerate, "gemini", "google", "google-generative-ai":
		return ProtocolGeminiGenerate, nil
	default:
		return "", fmt.Errorf("unsupported protocol %q", value)
	}
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("configuration contains multiple JSON values")
		}
		return err
	}
	return nil
}

func resolveValue(value, configDirectory string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if inner, ok := placeholder(value, "env"); ok {
		resolved, exists := os.LookupEnv(inner)
		if !exists || strings.TrimSpace(resolved) == "" {
			return "", fmt.Errorf("environment variable %s is unavailable", inner)
		}
		return strings.TrimSpace(resolved), nil
	}
	if inner, ok := placeholder(value, "file"); ok {
		path := inner
		if !filepath.IsAbs(path) {
			path = filepath.Join(configDirectory, path)
		}
		content, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return "", err
		}
		resolved := strings.TrimSpace(string(content))
		if resolved == "" {
			return "", errors.New("referenced file is empty")
		}
		return resolved, nil
	}
	return value, nil
}

func resolveValues(values map[string]string, configDirectory string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	resolved := make(map[string]string, len(values))
	for name, value := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, errors.New("header name is required")
		}
		resolvedValue, err := resolveValue(value, configDirectory)
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", name, err)
		}
		resolved[name] = resolvedValue
	}
	return resolved, nil
}

func placeholder(value, kind string) (string, bool) {
	prefix := "{" + kind + ":"
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "}") {
		return "", false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, prefix), "}"))
	return inner, inner != ""
}
