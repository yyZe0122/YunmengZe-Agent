package providerconfig

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvFilename is an optional KEY=value file under ConfigDir.
// Installers may seed it; ymz load it before resolving {env:…} keys.
// Not required: users may set process env, use {file:…}, or put a literal apiKey in JSON.
const EnvFilename = "env"

// LoadEnvFile reads path as KEY=value lines and sets process environment for keys
// that are unset or empty. Existing non-empty env wins. Missing file is a no-op.
// Lines starting with # and blank lines are ignored. Values may be single- or double-quoted.
func LoadEnvFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open env file %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow long values (API keys).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t") {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if cur, exists := os.LookupEnv(key); exists && strings.TrimSpace(cur) != "" {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return fmt.Errorf("set env %s from %s:%d: %w", key, path, lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read env file %s: %w", path, err)
	}
	return nil
}

// LoadEnvFromConfigDir loads ConfigDir/env if present.
func LoadEnvFromConfigDir(configDir string) error {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return nil
	}
	return LoadEnvFile(filepath.Join(configDir, EnvFilename))
}

// EnsureEnvFile writes a template env file when missing (mode 0600).
func EnsureEnvFile(configDir string) (created bool, path string, err error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return false, "", fmt.Errorf("config directory is required")
	}
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return false, "", fmt.Errorf("create config directory %s: %w", configDir, err)
	}
	path = filepath.Join(configDir, EnvFilename)
	if _, statErr := os.Stat(path); statErr == nil {
		return false, path, nil
	} else if !os.IsNotExist(statErr) {
		return false, path, fmt.Errorf("inspect env file %s: %w", path, statErr)
	}
	if err := os.WriteFile(path, []byte(defaultEnvTemplate), 0o600); err != nil {
		return false, path, fmt.Errorf("write env template %s: %w", path, err)
	}
	return true, path, nil
}

// defaultEnvTemplate is optional; empty values are fine. Fill any keys you use with {env:NAME} in config.
const defaultEnvTemplate = `# YunmengZe optional environment file (KEY=value).
# Loaded when the ymz starts; does not override variables already set in the process.
# Pair with config apiKey "{env:DEEPSEEK1_API_KEY}" / "{env:DEEPSEEK2_API_KEY}" (recommended), or literal / {file:…}.
# Keep this file private (mode 600). Do not commit secrets.
#
DEEPSEEK1_API_KEY=
DEEPSEEK2_API_KEY=
OPENAI_API_KEY=
ANTHROPIC_API_KEY=
GEMINI_API_KEY=
`
