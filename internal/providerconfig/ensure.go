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

1. 按用户目标完成工作，先查看现有实现，再做必要的修改。
2. plan 只读分析；agent 直接执行工具，不逐次请求授权。
3. 简洁说明结果、验证方法和仍未解决的问题。
4. 不覆盖用户已有改动；遇到影响任务方向的信息缺口，用 ask_user 澄清。
5. 专项流程可用 skills_list / skill_view 加载；文件操作优先使用 read / write / edit，命令使用 shell。
`

// defaultConfigTemplate is written when ConfigDir has no config and nothing to migrate.
// Keys use {env:…}; no literal secrets.
const defaultConfigTemplate = `{
  "model": "deepseek1/deepseek-chat",
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
    }
  }
}
`
