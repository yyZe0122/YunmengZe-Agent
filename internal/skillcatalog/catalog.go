// Package skillcatalog discovers file-based skills and reads their content on demand.
package skillcatalog

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/yyZe0122/yunmengze-agent/internal/injectscan"
	"github.com/yyZe0122/yunmengze-agent/internal/platform/pathsecurity"
)

const (
	SkillFileName = "SKILL.md"
)

var (
	ErrSkillNotFound = errors.New("skill not found")
	ErrInvalidSkill  = errors.New("invalid skill")
)

type Source string

const (
	SourceUser    Source = "user"
	SourceProject Source = "project"
)

// Root is a skill directory and its label. Roots are evaluated from low to
// high priority; a later root replaces an earlier skill with the same ID.
type Root struct {
	Path   string
	Source Source
}

type Skill struct {
	ID          string
	Name        string
	Description string
	FilePath    string
	Source      Source
	rootPath    string
}

type Diagnostic struct {
	Path string
	Err  error
}

func (d Diagnostic) Error() string {
	if d.Path == "" {
		return d.Err.Error()
	}
	return d.Path + ": " + d.Err.Error()
}

type Catalog struct {
	byID  map[string]Skill
	order []string
}

// Discover scans <root>/<skill-id>/SKILL.md entries. Discovery reads only the
// frontmatter needed for the catalog; skill bodies are loaded by Read.
func Discover(roots []Root) (*Catalog, []Diagnostic) {
	catalog := &Catalog{byID: make(map[string]Skill)}
	var diagnostics []Diagnostic

	for _, configured := range roots {
		rootPath := strings.TrimSpace(configured.Path)
		if rootPath == "" {
			diagnostics = append(diagnostics, Diagnostic{Err: errors.New("skill root path is required")})
			continue
		}
		absolute, err := filepath.Abs(rootPath)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Path: rootPath, Err: err})
			continue
		}
		resolvedRoot, err := pathsecurity.ResolveExisting(absolute)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Path: absolute, Err: err})
			continue
		}
		entries, err := os.ReadDir(resolvedRoot)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Path: resolvedRoot, Err: err})
			continue
		}

		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				diagnostics = append(diagnostics, Diagnostic{Path: filepath.Join(resolvedRoot, entry.Name()), Err: errors.New("skill directory symlink is not allowed")})
				continue
			}
			if !entry.IsDir() {
				continue
			}
			filePath := filepath.Join(resolvedRoot, entry.Name(), SkillFileName)
			info, err := os.Lstat(filePath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				diagnostics = append(diagnostics, Diagnostic{Path: filePath, Err: err})
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				diagnostics = append(diagnostics, Diagnostic{Path: filePath, Err: errors.New("skill file symlink is not allowed")})
				continue
			}
			if !info.Mode().IsRegular() || !pathsecurity.ContainsResolved(resolvedRoot, filePath) {
				diagnostics = append(diagnostics, Diagnostic{Path: filePath, Err: errors.New("skill file is outside its root or is not regular")})
				continue
			}
			metadata, err := readMetadata(filePath)
			if err != nil {
				diagnostics = append(diagnostics, Diagnostic{Path: filePath, Err: err})
				continue
			}
			id := strings.TrimSpace(entry.Name())
			if id == "" || id == "." || id == ".." {
				diagnostics = append(diagnostics, Diagnostic{Path: filePath, Err: fmt.Errorf("%w: invalid directory ID", ErrInvalidSkill)})
				continue
			}
			catalog.byID[id] = Skill{
				ID:          id,
				Name:        metadata.name,
				Description: metadata.description,
				FilePath:    filePath,
				Source:      configured.Source,
				rootPath:    resolvedRoot,
			}
		}
	}

	catalog.order = make([]string, 0, len(catalog.byID))
	for id := range catalog.byID {
		catalog.order = append(catalog.order, id)
	}
	sort.Strings(catalog.order)
	return catalog, diagnostics
}

func (c *Catalog) Skills() []Skill {
	skills := make([]Skill, 0, len(c.order))
	for _, id := range c.order {
		skills = append(skills, c.byID[id])
	}
	return skills
}

// Get returns a discovered skill by ID.
func (c *Catalog) Get(id string) (Skill, bool) {
	sk, ok := c.byID[strings.TrimSpace(id)]
	return sk, ok
}

// Read loads the current body for a discovered skill and rechecks containment.
func (c *Catalog) Read(id string) ([]byte, error) {
	skill, ok := c.byID[strings.TrimSpace(id)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSkillNotFound, strings.TrimSpace(id))
	}
	return readSkillBody(skill)
}

func readSkillBody(skill Skill) ([]byte, error) {
	info, err := os.Lstat(skill.FilePath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !pathsecurity.ContainsResolved(skill.rootPath, skill.FilePath) {
		return nil, fmt.Errorf("%w: skill path is no longer safe", ErrInvalidSkill)
	}
	if info.Size() > 1<<20 {
		return nil, fmt.Errorf("%w: skill exceeds 1 MiB", ErrInvalidSkill)
	}
	data, err := readBounded(skill.FilePath)
	if err != nil {
		return nil, err
	}
	body, err := splitSkillFile(data)
	if err != nil {
		return nil, err
	}
	if err := injectscan.Scan(body); err != nil {
		return nil, fmt.Errorf("%w: skill %q body: %v", ErrInvalidSkill, skill.ID, err)
	}
	return []byte(body), nil
}

// ReadLinked loads a regular file under the skill directory. Rejects traversal, symlinks, and SKILL.md itself.
func (c *Catalog) ReadLinked(id, rel string) ([]byte, error) {
	skill, err := c.lookup(id)
	if err != nil {
		return nil, err
	}
	rel = strings.TrimSpace(rel)
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return nil, fmt.Errorf("%w: linked path is required and must be relative", ErrInvalidSkill)
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%w: linked path escapes skill directory", ErrInvalidSkill)
	}
	slash := filepath.ToSlash(clean)
	if skipLinkedName(slash) {
		return nil, fmt.Errorf("%w: use skill_view without file_path to load SKILL.md", ErrInvalidSkill)
	}
	dir := filepath.Dir(skill.FilePath)
	target := filepath.Join(dir, filepath.FromSlash(slash))
	info, err := os.Lstat(target)
	if err != nil {
		return nil, fmt.Errorf("%w: linked file %q: %v", ErrSkillNotFound, slash, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: linked path is not a regular file", ErrInvalidSkill)
	}
	if !pathsecurity.ContainsResolved(skill.rootPath, target) || !pathsecurity.ContainsResolved(dir, target) {
		return nil, fmt.Errorf("%w: linked path is outside the skill directory", ErrInvalidSkill)
	}
	if info.Size() > 1<<20 {
		return nil, fmt.Errorf("%w: linked file exceeds 1 MiB", ErrInvalidSkill)
	}
	data, err := readBounded(target)
	if err != nil {
		return nil, err
	}
	if err := injectscan.Scan(string(data)); err != nil {
		return nil, fmt.Errorf("%w: skill %q file %q: %v", ErrInvalidSkill, skill.ID, slash, err)
	}
	return data, nil
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("%w: skill file exceeds 1 MiB", ErrInvalidSkill)
	}
	return data, nil
}

func (c *Catalog) lookup(id string) (Skill, error) {
	skill, ok := c.byID[strings.TrimSpace(id)]
	if !ok {
		return Skill{}, fmt.Errorf("%w: %s", ErrSkillNotFound, strings.TrimSpace(id))
	}
	return skill, nil
}

func skipLinkedName(rel string) bool {
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case SkillFileName, "SKILL.md.draft":
		return true
	}
	return strings.HasPrefix(base, SkillFileName+".bak.")
}

type metadata struct {
	name        string
	description string
}

func readMetadata(filePath string) (metadata, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return metadata{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return metadata{}, fmt.Errorf("%w: missing frontmatter", ErrInvalidSkill)
	}
	values := make(map[string]string)
	closed := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			closed = true
			break
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, found := strings.Cut(line, ":")
		if !found {
			return metadata{}, fmt.Errorf("%w: invalid frontmatter line %q", ErrInvalidSkill, line)
		}
		key = strings.TrimSpace(key)
		if key != "name" && key != "description" {
			continue
		}
		value, err := parseScalar(strings.TrimSpace(raw))
		if err != nil {
			return metadata{}, fmt.Errorf("%w: %s: %v", ErrInvalidSkill, key, err)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return metadata{}, err
	}
	if !closed {
		return metadata{}, fmt.Errorf("%w: unterminated frontmatter", ErrInvalidSkill)
	}
	name := strings.TrimSpace(values["name"])
	description := strings.TrimSpace(values["description"])
	if name == "" || description == "" {
		return metadata{}, fmt.Errorf("%w: name and description are required", ErrInvalidSkill)
	}
	return metadata{name: name, description: description}, nil
}

func parseScalar(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "`") {
		return strconv.Unquote(value)
	}
	if strings.HasPrefix(value, "'") {
		if len(value) < 2 || !strings.HasSuffix(value, "'") {
			return "", errors.New("unterminated quoted value")
		}
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
	}
	return value, nil
}

func splitSkillFile(data []byte) (string, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return "", fmt.Errorf("%w: missing frontmatter", ErrInvalidSkill)
	}
	closing := -1
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			closing = index
			break
		}
	}
	if closing < 0 {
		return "", fmt.Errorf("%w: unterminated frontmatter", ErrInvalidSkill)
	}
	return strings.Join(lines[closing+1:], "\n"), nil
}
