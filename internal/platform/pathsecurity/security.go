// Package pathsecurity provides shared lexical and symlink-aware path checks.
package pathsecurity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveExisting resolves symlinks in the existing portion of an absolute
// path and then appends any missing suffix. Callers should use the result
// promptly because filesystem checks cannot eliminate TOCTOU races.
func ResolveExisting(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("path is required")
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("path must be absolute")
	}
	current := filepath.Clean(value)
	missing := make([]string, 0)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			real, err := resolveSymlinks(current, 0)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return filepath.Clean(real), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("find existing path ancestor: %w", err)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// resolveSymlinks walks path components with Lstat instead of requiring a
// directory listing. This works in restricted service accounts where the
// process can access a known path but cannot enumerate every ancestor.
func resolveSymlinks(value string, depth int) (string, error) {
	if depth > 255 {
		return "", errors.New("too many symbolic links")
	}
	value = filepath.Clean(value)
	volume := filepath.VolumeName(value)
	root := string(filepath.Separator)
	if volume != "" {
		root = volume + string(filepath.Separator)
	}
	remainder := strings.TrimPrefix(value, root)
	current := root
	if remainder == "" {
		return current, nil
	}
	for _, component := range strings.Split(remainder, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		candidate := filepath.Join(current, component)
		info, err := os.Lstat(candidate)
		if err != nil {
			return "", err
		}
		target, readErr := os.Readlink(candidate)
		if readErr != nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", readErr
			}
			current = candidate
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(candidate), target)
		}
		current, err = resolveSymlinks(target, depth+1)
		if err != nil {
			return "", err
		}
	}
	return filepath.Clean(current), nil
}

// Contains reports whether target is root itself or a descendant of root.
func Contains(root, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

// ContainsResolved performs containment after resolving symlinks in both
// paths, including when their final components do not yet exist.
func ContainsResolved(root, target string) bool {
	resolvedRoot, err := ResolveExisting(root)
	if err != nil {
		return false
	}
	resolvedTarget, err := ResolveExisting(target)
	if err != nil {
		return false
	}
	return Contains(resolvedRoot, resolvedTarget)
}
