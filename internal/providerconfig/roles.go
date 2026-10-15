package providerconfig

import (
	"errors"
	"strings"
)

// LoadModelRoles returns the default model and configured role overrides.
func LoadModelRoles(configDir string) (main string, roles map[string]string, err error) {
	path, err := findConfigPath(configDir)
	if err != nil {
		return "", nil, err
	}
	if path == "" {
		return "", nil, nil
	}
	config, err := decodeConfigFile(path)
	if err != nil {
		return "", nil, err
	}
	if err := validateModelsMap(config); err != nil {
		return "", nil, err
	}
	main = strings.TrimSpace(config.Model)
	if len(config.Models) == 0 {
		return main, nil, nil
	}
	roles = make(map[string]string, len(config.Models))
	for role, ref := range config.Models {
		role = strings.TrimSpace(role)
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		roles[role] = ref
	}
	if len(roles) == 0 {
		return main, nil, nil
	}
	return main, roles, nil
}

// ResolveModel loads configuration and resolves provider/model for the given ref.
// ref must be provider/model. The selected top-level model field is overridden.
func ResolveModel(configDir, ref string) (*Resolved, error) {
	if err := LoadEnvFromConfigDir(configDir); err != nil {
		return nil, err
	}
	ref = strings.TrimSpace(ref)
	providerID, modelID, ok := strings.Cut(ref, "/")
	providerID, modelID = strings.TrimSpace(providerID), strings.TrimSpace(modelID)
	if !ok || providerID == "" || modelID == "" {
		return nil, errors.New("model must use provider/model format")
	}
	path, err := findConfigPath(configDir)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("provider config not found")
	}
	resolved, err := loadFileWithModel(path, providerID, modelID)
	if err != nil {
		return nil, err
	}
	return &resolved, nil
}
