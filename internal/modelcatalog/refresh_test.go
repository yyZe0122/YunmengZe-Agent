package modelcatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshWritesCacheAndSwaps(t *testing.T) {
	body := `{
  "lab/demo-model": {"id":"lab/demo-model","limit":{"context":12345,"output":678}}
}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user-agent")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	orig := SourceURL
	SourceURL = server.URL
	t.Cleanup(func() { SourceURL = orig })

	root := t.TempDir()
	t.Cleanup(func() { loaded.Store(nil) })
	if err := Refresh(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	lim := Lookup("demo-model")
	if lim.Context != 12345 || lim.Output != 678 {
		t.Fatalf("after refresh %+v", lim)
	}
	cached, err := os.ReadFile(filepath.Join(root, CacheDirName, CacheFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) == 0 {
		t.Fatal("empty cache")
	}
}

func TestInitPrefersCache(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, CacheDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(embeddedModels, &doc); err != nil {
		t.Fatal(err)
	}
	doc["lab/cached-only"] = json.RawMessage(`{"id":"lab/cached-only","limit":{"context":4242,"output":99}}`)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, CacheFilename), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded.Store(nil)
	Init(root)
	t.Cleanup(func() { loaded.Store(nil) })
	lim := Lookup("cached-only")
	if lim.Context != 4242 {
		t.Fatalf("cache lookup = %+v", lim)
	}
}

func TestInitIgnoresSmallerCache(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, CacheDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"lab/tiny":{"id":"lab/tiny","limit":{"context":7,"output":7}}}`)
	if err := os.WriteFile(filepath.Join(dir, CacheFilename), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded.Store(nil)
	Init(root)
	t.Cleanup(func() { loaded.Store(nil) })
	if Lookup("tiny").Context == 7 {
		t.Fatal("smaller cache must not override embed")
	}
	if Lookup("deepseek-v4-flash").Context < 100_000 {
		t.Fatal("embed should remain after smaller cache")
	}
}
