package providerconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/platform/pathsecurity"
)

// AppendWorkspaceAllow merges an extra absolute root into agent.local.json
// chat.workspace.allow (creating local from agent.json when needed).
func AppendWorkspaceAllow(configDir, extraRoot string) (string, error) {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return "", errors.New("config directory is required")
	}
	extraRoot = strings.TrimSpace(extraRoot)
	if err := pathsecurity.ValidExtraRoot(extraRoot); err != nil {
		return "", err
	}
	extraRoot = filepath.Clean(extraRoot)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	localPath := filepath.Join(configDir, LocalFilename)
	file, err := loadOrSeedLocal(configDir, localPath)
	if err != nil {
		return "", err
	}
	if file.Chat == nil {
		file.Chat = &ChatConfig{}
	}
	if file.Chat.Workspace == nil {
		file.Chat.Workspace = &ChatWorkspaceConfig{}
	}
	for _, existing := range file.Chat.Workspace.Allow {
		if filepath.Clean(strings.TrimSpace(existing)) == extraRoot {
			return localPath, nil
		}
	}
	file.Chat.Workspace.Allow = append(file.Chat.Workspace.Allow, extraRoot)
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(configDir, ".yunmengze-allow-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, localPath); err != nil {
		return "", fmt.Errorf("replace agent.local.json: %w", err)
	}
	cleanup = false
	return localPath, nil
}

func loadOrSeedLocal(configDir, localPath string) (File, error) {
	if _, err := os.Stat(localPath); err == nil {
		return decodeConfigFile(localPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return File{}, fmt.Errorf("stat agent.local.json: %w", err)
	}
	base := filepath.Join(configDir, Filename)
	if _, err := os.Stat(base); err == nil {
		return decodeConfigFile(base)
	} else if !errors.Is(err, os.ErrNotExist) {
		return File{}, fmt.Errorf("stat agent.json: %w", err)
	}
	return File{}, nil
}
