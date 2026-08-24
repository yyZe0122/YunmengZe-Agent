// Package architecture holds import-boundary guards (ADR-022 / G4).
package architecture_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPkgDoesNotImportInternal keeps pkg/* free of internal/* (stable contracts).
func TestPkgDoesNotImportInternal(t *testing.T) {
	root := moduleRoot(t)
	pkgDir := filepath.Join(root, "pkg")
	var offenders []string
	err := filepath.WalkDir(pkgDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		imports := parseImports(t, path)
		for _, imp := range imports {
			if strings.Contains(imp, "/internal/") || strings.HasSuffix(imp, "/internal") {
				offenders = append(offenders, path+": "+imp)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("pkg/* must not import internal/*:\n%s", strings.Join(offenders, "\n"))
	}
}

// TestGatewayPackagesAvoidSQL keeps HTTP adapters off business SQLite.
func TestGatewayPackagesAvoidSQL(t *testing.T) {
	root := moduleRoot(t)
	for _, rel := range []string{
		"internal/gateway",
		"internal/gatewayclient",
		"internal/tasksubmission",
		"internal/scheduledtasks",
	} {
		dir := filepath.Join(root, rel)
		var offenders []string
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			for _, imp := range parseImports(t, path) {
				if imp == "database/sql" || strings.Contains(imp, "store/sqlite") || strings.HasSuffix(imp, "/sqlite") && strings.Contains(imp, "modernc.org") {
					// modernc is only allowed via store/sqlite composition root — flag direct use.
					if imp == "database/sql" || strings.Contains(imp, "store/sqlite") {
						offenders = append(offenders, path+": "+imp)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(offenders) > 0 {
			t.Fatalf("%s must not import database/sql or store/sqlite:\n%s", rel, strings.Join(offenders, "\n"))
		}
	}
}

// TestTUIDoesNotImportDaemonInternals keeps the TUI on gatewayclient only.
func TestTUIDoesNotImportDaemonInternals(t *testing.T) {
	assertNoForbiddenImports(t, "internal/tui", clientForbiddenImports())
}

// TestCLIDoesNotImportDaemonInternals keeps ymz off tools/provider/agent.
func TestCLIDoesNotImportDaemonInternals(t *testing.T) {
	assertNoForbiddenImports(t, "cmd/ymz", clientForbiddenImports())
}

// TestGatewayDoesNotImportBrokerOrProvider keeps HTTP adapters off effect paths.
func TestGatewayDoesNotImportBrokerOrProvider(t *testing.T) {
	assertNoForbiddenImports(t, "internal/gateway", []string{
		"/internal/tools",
		"/internal/agent",
		"/internal/providerruntime",
		"/internal/providers",
	})
}

func clientForbiddenImports() []string {
	return []string{
		"/internal/tools",
		"/internal/agent",
		"/internal/chatsession",
		"/internal/sessiontodo",
		"/internal/store/sqlite",
		"/internal/providerruntime",
		"/internal/providers",
	}
}

func assertNoForbiddenImports(t *testing.T, rel string, forbidden []string) {
	t.Helper()
	root := moduleRoot(t)
	dir := filepath.Join(root, rel)
	var offenders []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		for _, imp := range parseImports(t, path) {
			for _, needle := range forbidden {
				if strings.Contains(imp, needle) || strings.HasSuffix(imp, needle) {
					offenders = append(offenders, path+": "+imp)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("%s must not import daemon internals:\n%s", rel, strings.Join(offenders, "\n"))
	}
}

// TestGatewayClientDoesNotImportGatewayServer enforces G2 package boundary.
func TestGatewayClientDoesNotImportGatewayServer(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, "internal/gatewayclient")
	var offenders []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		for _, imp := range parseImports(t, path) {
			if strings.HasSuffix(imp, "/internal/gateway") {
				offenders = append(offenders, path+": "+imp)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("gatewayclient must not import gateway server:\n%s", strings.Join(offenders, "\n"))
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// test runs from internal/architecture
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found from %s: %v", wd, err)
	}
	return root
}

func parseImports(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	// Strip block comments lightly to avoid false positives in docs.
	var imports []string
	inImport := false
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "import (") {
			inImport = true
			continue
		}
		if inImport {
			if trim == ")" {
				inImport = false
				continue
			}
			if trim == "" || strings.HasPrefix(trim, "//") {
				continue
			}
			// `alias "path"` or `"path"`
			if i := strings.Index(trim, "\""); i >= 0 {
				rest := trim[i+1:]
				if j := strings.Index(rest, "\""); j >= 0 {
					imports = append(imports, rest[:j])
				}
			}
			continue
		}
		if strings.HasPrefix(trim, "import \"") {
			if i := strings.Index(trim, "\""); i >= 0 {
				rest := trim[i+1:]
				if j := strings.Index(rest, "\""); j >= 0 {
					imports = append(imports, rest[:j])
				}
			}
		}
	}
	return imports
}
