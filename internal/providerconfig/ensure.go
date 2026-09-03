package providerconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func findConfigPath(configDir string) (string, error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return "", errors.New("config directory is required")
	}
	candidates := []string{
		filepath.Join(configDir, LocalFilename),
		filepath.Join(configDir, Filename),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect provider config %s: %w", candidate, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("provider config is not a regular file: %s", candidate)
		}
		return candidate, nil
	}
	return "", nil
}

// EnsureResult describes how provider config was made available under configDir.
type EnsureResult struct {
	Path    string // active config path under configDir (may be empty only on error)
	Created bool   // wrote the default template
}

// EnsureConfig prepares configDir for provider loading:
//  1. MkdirAll configDir
//  2. If ConfigDir already has a config file, use it
//  3. Else write a default template (env-based keys, no secrets) as Filename
func EnsureConfig(configDir string) (EnsureResult, error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return EnsureResult{}, errors.New("config directory is required")
	}
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return EnsureResult{}, fmt.Errorf("create config directory %s: %w", configDir, err)
	}
	// Optional ConfigDir/env (installers may seed it). Never fails the ensure path on load errors beyond I/O.
	if err := LoadEnvFromConfigDir(configDir); err != nil {
		return EnsureResult{}, err
	}
	if _, _, err := EnsureEnvFile(configDir); err != nil {
		return EnsureResult{}, err
	}
	if _, err := EnsureAgentsFile(configDir); err != nil {
		return EnsureResult{}, err
	}
	existing, err := findConfigPath(configDir)
	if err != nil {
		return EnsureResult{}, err
	}
	if existing != "" {
		return EnsureResult{Path: existing}, nil
	}
	dst := filepath.Join(configDir, Filename)
	if err := os.WriteFile(dst, []byte(defaultConfigTemplate), 0o600); err != nil {
		return EnsureResult{}, fmt.Errorf("write default provider config %s: %w", dst, err)
	}
	return EnsureResult{Path: dst, Created: true}, nil
}

// EnsureAgentsFile writes ConfigDir/AGENTS.md when missing. Existing files are left untouched.
func EnsureAgentsFile(configDir string) (created bool, err error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return false, errors.New("config directory is required")
	}
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return false, fmt.Errorf("create config directory %s: %w", configDir, err)
	}
	path := filepath.Join(configDir, AgentsFilename)
	info, statErr := os.Lstat(path)
	if statErr == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("AGENTS.md is not a regular file: %s", path)
		}
		return false, nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("inspect AGENTS.md %s: %w", path, statErr)
	}
	if err := os.WriteFile(path, []byte(defaultAgentsMarkdown), 0o600); err != nil {
		return false, fmt.Errorf("write AGENTS.md %s: %w", path, err)
	}
	return true, nil
}

const defaultAgentsMarkdown = `# Agent rules

1. 用户没要求的不要做；要做先问清楚并得到允许。
2. 只在当前项目/工作区目录内操作；需要出界先征得同意。
3. 少废话，直接做事。
4. 仅删除本轮自己产生、且 100% 可确认是垃圾的文件（如自己写的临时/失败草稿）；拿不准的先问用户，未允许不删。
5. 专项流程先 skills_list 再 skill_view；已配置的 mcp_* 优先于自己写脚本或 process_exec。
`

// defaultConfigTemplate is written when ConfigDir has no config and nothing to migrate.
// Keys use {env:…}; no literal secrets.
const defaultConfigTemplate = `{
  "model": "deepseek1/deepseek-chat",
  "models": {
    "subagent": "deepseek1/deepseek-chat",
    "compact": "deepseek1/deepseek-chat"
  },
  "provider": {
    "deepseek1": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://api.deepseek.com/v1",
        "apiKey": "{env:DEEPSEEK1_API_KEY}"
      },
      "models": {
        "deepseek-chat": {
          "name": "DeepSeek Chat"
        }
      }
    },
    "deepseek2": {
      "type": "openai-compatible",
      "options": {
        "baseURL": "https://llm.example.com/v1",
        "apiKey": "{env:DEEPSEEK2_API_KEY}"
      },
      "models": {
        "deepseek/deepseek-v4-flash": {
          "name": "Nested wire id (select deepseek2/deepseek/deepseek-v4-flash)"
        },
        "flash": {
          "name": "Short key + id override (select deepseek2/flash)",
          "id": "deepseek/deepseek-v4-flash"
        }
      }
    }
  }
}
`
