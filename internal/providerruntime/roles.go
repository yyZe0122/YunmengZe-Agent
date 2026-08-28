package providerruntime

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/agent"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/internal/providers"
)

// BuildRoleEndpoints resolves optional models.subagent / compact / web / vision (ADR-045).
// models.speech is Whisper-only (ADR-055) and is never a chat RoleEndpoint.
// Unset roles are omitted so agent falls back to main. Not updated by hot-reload.
func BuildRoleEndpoints(configDir, mainRef string) (map[string]agent.RoleEndpoint, error) {
	_, roles, err := providerconfig.LoadModelRoles(configDir)
	if err != nil {
		return nil, fmt.Errorf("load model roles: %w", err)
	}
	if len(roles) == 0 {
		return nil, nil
	}
	cache := map[string]agent.RoleEndpoint{}
	out := make(map[string]agent.RoleEndpoint, len(roles))
	for role, ref := range roles {
		role = strings.TrimSpace(role)
		ref = strings.TrimSpace(ref)
		if ref == "" || ref == mainRef {
			continue
		}
		if role == providerconfig.RoleSpeech {
			continue
		}
		if ep, ok := cache[ref]; ok {
			out[role] = ep
			continue
		}
		resolved, err := providerconfig.ResolveModel(configDir, ref)
		if err != nil {
			return nil, fmt.Errorf("resolve models.%s: %w", role, err)
		}
		provider, err := providers.NewConfigured(*resolved)
		if err != nil {
			return nil, fmt.Errorf("configure models.%s: %w", role, err)
		}
		ep := agent.RoleEndpoint{
			Provider: provider, Model: resolved.ModelID, ContextWindow: resolved.ContextWindow,
		}
		cache[ref] = ep
		out[role] = ep
		slog.Info("model role configured", "component", "daemon", "operation", "model_roles", "result", "succeeded",
			"role", role, "model", ref, "context_window", resolved.ContextWindow)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
