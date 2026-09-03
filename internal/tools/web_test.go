package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
)

func TestParseDDGHTML(t *testing.T) {
	htmlBody := `<html><body>
<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fdocs">Example Docs</a>
<div class="result__snippet">Official docs</div>
</body></html>`
	hits, err := parseDDGHTML([]byte(htmlBody), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].URL != "https://example.com/docs" || hits[0].Title != "Example Docs" {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestWebSearchDDG(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		w.Write([]byte(`<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev">Go</a>`))
	}))
	t.Cleanup(srv.Close)
	tool := newWebSearchTool(webSearchConfig{backend: providerconfig.WebSearchDDG}).(*webSearchTool)
	tool.ddgURL = srv.URL
	tool.checkHost = func(context.Context, string) error { return nil }
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "https://go.dev") {
		t.Fatalf("out = %s", out)
	}
	auth, err := tool.Authorization(context.Background(), json.RawMessage(`{"query":"go"}`))
	if err != nil || auth.NetworkDomain != ddgSearchHost {
		t.Fatalf("auth = %+v err=%v", auth, err)
	}
}

func TestWebSearchSearxNGAndTavily(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"A","url":"https://a.example","content":"alpha"}]}`))
	}))
	t.Cleanup(searx.Close)
	cfg, err := newWebSearchConfig(providerconfig.ChatConfig{Web: &providerconfig.ChatWebConfig{
		Search: providerconfig.WebSearchSearxNG, SearxNGURL: searx.URL,
	}})
	if err != nil {
		t.Fatal(err)
	}
	tool := newWebSearchTool(cfg).(*webSearchTool)
	tool.checkHost = func(context.Context, string) error { return nil }
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"alpha","limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "https://a.example") {
		t.Fatalf("searx = %s", out)
	}

	tav := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["api_key"] != "secret" {
			t.Fatalf("key missing: %#v", body)
		}
		w.Write([]byte(`{"results":[{"title":"B","url":"https://b.example","content":"beta"}]}`))
	}))
	t.Cleanup(tav.Close)
	tavily := newWebSearchTool(webSearchConfig{backend: providerconfig.WebSearchTavily, tavilyKey: "secret"}).(*webSearchTool)
	tavily.tavilyURL = tav.URL
	tavily.checkHost = func(context.Context, string) error { return nil }
	out, err = tavily.Execute(context.Background(), json.RawMessage(`{"query":"beta"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "https://b.example") {
		t.Fatalf("tavily = %s", out)
	}
}

func TestWebExtractHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><script>secret()</script></head><body><p>Hello <b>world</b></p></body></html>`))
	}))
	t.Cleanup(srv.Close)
	tool := newWebExtractTool(1024).(*webExtractTool)
	tool.checkHost = func(context.Context, string) error { return nil }
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Hello") || strings.Contains(string(out), "secret()") {
		t.Fatalf("out = %s", out)
	}
}

func TestTruncateRunesHeadTail(t *testing.T) {
	got := truncateRunesHeadTail(strings.Repeat("a", 200), 40)
	if !strings.Contains(got, "[TRUNCATED]") {
		t.Fatalf("got %q", got)
	}
}

func TestValidateHTTPURLHostAllowlist(t *testing.T) {
	if err := validateHTTPURLHost(context.Background(), "127.0.0.1", nil); err == nil {
		t.Fatal("loopback must block")
	}
	if err := validateHTTPURLHost(context.Background(), "127.0.0.1", map[string]struct{}{"127.0.0.1": {}}); err != nil {
		t.Fatalf("allowlist: %v", err)
	}
}

func TestNewWebSearchConfigFailClosed(t *testing.T) {
	if _, err := newWebSearchConfig(providerconfig.ChatConfig{Web: &providerconfig.ChatWebConfig{Search: "searxng"}}); err == nil {
		t.Fatal("expected missing searxng url")
	}
	if _, err := newWebSearchConfig(providerconfig.ChatConfig{Web: &providerconfig.ChatWebConfig{Search: "tavily"}}); err == nil {
		t.Fatal("expected missing tavily key")
	}
}

func TestRegisterWebToolsDefinitions(t *testing.T) {
	search := newWebSearchTool(webSearchConfig{backend: providerconfig.WebSearchDDG})
	extract := newWebExtractTool(1024)
	if search.Definition().Name != "web_search" || extract.Definition().Name != "web_extract" {
		t.Fatalf("names = %s %s", search.Definition().Name, extract.Definition().Name)
	}
}
