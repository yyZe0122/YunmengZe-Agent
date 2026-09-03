package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/yyZe0122/yunmengze-agent/internal/policy"
	"github.com/yyZe0122/yunmengze-agent/internal/providerconfig"
	"github.com/yyZe0122/yunmengze-agent/pkg/toolapi"
)

const (
	ddgSearchHost    = "html.duckduckgo.com"
	ddgSearchURL     = "https://html.duckduckgo.com/html/"
	tavilySearchURL  = "https://api.tavily.com/search"
	tavilyHost       = "api.tavily.com"
	webSearchUA      = "Mozilla/5.0 (compatible; YunmengZe/0; +https://github.com/yyZe0122/yunmengze-agent)"
	webExtractUA     = "YunmengZe/0"
	webExtractCap    = 32 * 1024
	webSearchMax     = 20
	webSearchDefault = 8
)

type webSearchConfig struct {
	backend     string
	searxngURL  string
	searxngHost string
	tavilyKey   string
}

func newWebSearchConfig(chat providerconfig.ChatConfig) (webSearchConfig, error) {
	cfg := webSearchConfig{backend: chat.WebSearchBackend()}
	switch cfg.backend {
	case providerconfig.WebSearchDDG:
		return cfg, nil
	case providerconfig.WebSearchSearxNG:
		raw := ""
		if chat.Web != nil {
			raw = strings.TrimSpace(chat.Web.SearxNGURL)
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return webSearchConfig{}, errors.New("chat.web.searxng_url must be an absolute http(s) URL")
		}
		if parsed.User != nil {
			return webSearchConfig{}, errors.New("chat.web.searxng_url must not include userinfo")
		}
		scheme := strings.ToLower(parsed.Scheme)
		if scheme != "http" && scheme != "https" {
			return webSearchConfig{}, errors.New("chat.web.searxng_url scheme must be http or https")
		}
		cfg.searxngURL = strings.TrimRight(raw, "/")
		cfg.searxngHost = strings.ToLower(parsed.Hostname())
		return cfg, nil
	case providerconfig.WebSearchTavily:
		key := ""
		if chat.Web != nil {
			key = strings.TrimSpace(chat.Web.TavilyKey)
		}
		if key == "" {
			return webSearchConfig{}, errors.New("chat.web.tavily_key is required when search is tavily")
		}
		cfg.tavilyKey = key
		return cfg, nil
	default:
		return webSearchConfig{}, fmt.Errorf("unsupported chat.web.search %q", cfg.backend)
	}
}

func (c webSearchConfig) permHost() string {
	switch c.backend {
	case providerconfig.WebSearchSearxNG:
		return c.searxngHost
	case providerconfig.WebSearchTavily:
		return tavilyHost
	default:
		return ddgSearchHost
	}
}

func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("redirects are disabled; approve the target URL explicitly")
	}}
}

type webSearchTool struct {
	client    *http.Client
	cfg       webSearchConfig
	ddgURL    string
	tavilyURL string
	checkHost func(context.Context, string) error
}

func newWebSearchTool(cfg webSearchConfig) Tool {
	return &webSearchTool{client: noRedirectClient(), cfg: cfg}
}

func (t *webSearchTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name: "web_search", Description: "Search the web. Interactive agent waits /perm (similar = search backend host). Plan and cron never receive this grant. Use web_extract or http_get for a specific URL.",
		Risk: string(policy.RiskR2), DefaultTimeoutMillis: 30000,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["query"],"properties":{"query":{"type":"string","description":"Search query"},"limit":{"type":"integer","minimum":1,"maximum":20,"description":"Max results (default 8, cap 20)"}}}`),
	}
}

type webSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type webSearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func (t *webSearchTool) Authorization(_ context.Context, raw json.RawMessage) (Authorization, error) {
	input, err := t.parse(raw)
	if err != nil {
		return Authorization{}, err
	}
	_ = input
	return Authorization{Capability: "web_search", NetworkDomain: t.cfg.permHost()}, nil
}

func (t *webSearchTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	input, err := t.parse(raw)
	if err != nil {
		return nil, err
	}
	host := t.cfg.permHost()
	if err := t.hostOK(ctx, host); err != nil {
		return nil, err
	}
	hits, err := t.search(ctx, input.Query, input.Limit)
	if err != nil {
		return encodeResult(map[string]any{
			"error": "search_failed", "query": input.Query, "backend": t.cfg.backend,
			"message": err.Error(), "results": []webSearchHit{},
		})
	}
	if hits == nil {
		hits = []webSearchHit{}
	}
	hint := ""
	if len(hits) == 0 {
		hint = "No results. Try a shorter query or web_extract on a known URL."
	}
	return encodeResult(map[string]any{
		"query": input.Query, "backend": t.cfg.backend, "results": hits, "hint": hint,
	})
}

func (t *webSearchTool) hostOK(ctx context.Context, host string) error {
	if t.checkHost != nil {
		return t.checkHost(ctx, host)
	}
	return validateHTTPURLHost(ctx, host, t.ssrfAllow())
}

func (t *webSearchTool) ssrfAllow() map[string]struct{} {
	if t.cfg.backend != providerconfig.WebSearchSearxNG || t.cfg.searxngHost == "" {
		return nil
	}
	return map[string]struct{}{t.cfg.searxngHost: {}}
}

func (t *webSearchTool) parse(raw json.RawMessage) (webSearchInput, error) {
	var input webSearchInput
	if err := decodeStrict(raw, &input); err != nil {
		return input, err
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" {
		return input, errors.New("query is required")
	}
	if input.Limit < 0 {
		return input, errors.New("limit cannot be negative")
	}
	if input.Limit == 0 {
		input.Limit = webSearchDefault
	}
	if input.Limit > webSearchMax {
		input.Limit = webSearchMax
	}
	return input, nil
}

func (t *webSearchTool) search(ctx context.Context, query string, limit int) ([]webSearchHit, error) {
	switch t.cfg.backend {
	case providerconfig.WebSearchSearxNG:
		return t.searchSearxNG(ctx, query, limit)
	case providerconfig.WebSearchTavily:
		return t.searchTavily(ctx, query, limit)
	default:
		return t.searchDDG(ctx, query, limit)
	}
}

func (t *webSearchTool) searchDDG(ctx context.Context, query string, limit int) ([]webSearchHit, error) {
	form := url.Values{"q": {query}}
	endpoint := ddgSearchURL
	if t.ddgURL != "" {
		endpoint = t.ddgURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", webSearchUA)
	body, _, err := doHTTP(t.client, req, 2*1024*1024)
	if err != nil {
		return nil, err
	}
	return parseDDGHTML(body, limit)
}

func (t *webSearchTool) searchSearxNG(ctx context.Context, query string, limit int) ([]webSearchHit, error) {
	u, err := url.Parse(t.cfg.searxngURL + "/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", webSearchUA)
	body, _, err := doHTTP(t.client, req, 2*1024*1024)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("searxng json: %w", err)
	}
	out := make([]webSearchHit, 0, limit)
	for _, r := range parsed.Results {
		if len(out) >= limit {
			break
		}
		hit := webSearchHit{Title: strings.TrimSpace(r.Title), URL: strings.TrimSpace(r.URL), Snippet: strings.TrimSpace(r.Content)}
		if hit.URL == "" {
			continue
		}
		out = append(out, hit)
	}
	return out, nil
}

func (t *webSearchTool) searchTavily(ctx context.Context, query string, limit int) ([]webSearchHit, error) {
	payload, err := json.Marshal(map[string]any{"api_key": t.cfg.tavilyKey, "query": query, "max_results": limit})
	if err != nil {
		return nil, err
	}
	endpoint := tavilySearchURL
	if t.tavilyURL != "" {
		endpoint = t.tavilyURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", webSearchUA)
	body, _, err := doHTTP(t.client, req, 2*1024*1024)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("tavily json: %w", err)
	}
	out := make([]webSearchHit, 0, limit)
	for _, r := range parsed.Results {
		if len(out) >= limit {
			break
		}
		hit := webSearchHit{Title: strings.TrimSpace(r.Title), URL: strings.TrimSpace(r.URL), Snippet: strings.TrimSpace(r.Content)}
		if hit.URL == "" {
			continue
		}
		out = append(out, hit)
	}
	return out, nil
}

func parseDDGHTML(body []byte, limit int) ([]webSearchHit, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	out := make([]webSearchHit, 0, limit)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(out) >= limit {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			cls, href := "", ""
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "class":
					cls = a.Val
				case "href":
					href = strings.TrimSpace(a.Val)
				}
			}
			if strings.Contains(cls, "result__a") && href != "" {
				title := strings.TrimSpace(textOf(n))
				snippet := ""
				if parent := n.Parent; parent != nil {
					for sib := parent.NextSibling; sib != nil; sib = sib.NextSibling {
						if sib.Type == html.ElementNode && strings.Contains(attr(sib, "class"), "result__snippet") {
							snippet = strings.TrimSpace(textOf(sib))
							break
						}
					}
				}
				if u := unwrapDDGHref(href); u != "" {
					out = append(out, webSearchHit{Title: title, URL: u, Snippet: snippet})
				}
			}
		}
		for c := n.FirstChild; c != nil && len(out) < limit; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

func unwrapDDGHref(href string) string {
	parsed, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if uddg := parsed.Query().Get("uddg"); uddg != "" {
		if dest, err := url.QueryUnescape(uddg); err == nil && strings.HasPrefix(dest, "http") {
			return dest
		}
	}
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		if !strings.Contains(strings.ToLower(parsed.Host), "duckduckgo.com") {
			return href
		}
	}
	return ""
}

type webExtractTool struct {
	client       *http.Client
	maximumBytes int64
	checkHost    func(context.Context, string) error
}

func newWebExtractTool(maximumBytes int64) Tool {
	if maximumBytes <= 0 {
		maximumBytes = 16 * 1024 * 1024
	}
	return &webExtractTool{client: noRedirectClient(), maximumBytes: maximumBytes}
}

func (t *webExtractTool) Definition() toolapi.Definition {
	return toolapi.Definition{
		Name: "web_extract", Description: "HTTP/HTTPS GET a URL and return de-tagged text (no redirects). Interactive agent waits /perm (similar = that host). SSRF baseline blocks localhost/private/link-local. Plan and cron never receive this grant.",
		Risk: string(policy.RiskR2), DefaultTimeoutMillis: 30000,
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["url"],"properties":{"url":{"type":"string"}}}`),
	}
}

type webExtractInput struct {
	URL string `json:"url"`
}

func (t *webExtractTool) Authorization(_ context.Context, raw json.RawMessage) (Authorization, error) {
	_, parsed, err := t.parse(raw)
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{Capability: "web_extract", NetworkDomain: strings.ToLower(parsed.Hostname())}, nil
}

func (t *webExtractTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	input, parsed, err := t.parse(raw)
	if err != nil {
		return nil, err
	}
	if err := t.hostOK(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, input.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", webExtractUA)
	body, contentType, err := doHTTP(t.client, req, t.maximumBytes)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(extractReadableText(body, contentType))
	truncated := false
	if utf8.RuneCountInString(text) > webExtractCap {
		text = truncateRunesHeadTail(text, webExtractCap)
		truncated = true
	}
	return encodeResult(map[string]any{
		"url": input.URL, "content_type": contentType, "text": text, "truncated": truncated,
	})
}

func (t *webExtractTool) hostOK(ctx context.Context, host string) error {
	if t.checkHost != nil {
		return t.checkHost(ctx, host)
	}
	return validateHTTPGetURLHost(ctx, host)
}

func (t *webExtractTool) parse(raw json.RawMessage) (webExtractInput, *url.URL, error) {
	var input webExtractInput
	if err := decodeStrict(raw, &input); err != nil {
		return input, nil, err
	}
	parsed, err := url.Parse(strings.TrimSpace(input.URL))
	if err != nil {
		return input, nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return input, nil, fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return input, nil, errors.New("URL host is required and userinfo is forbidden")
	}
	if t.checkHost == nil {
		host := strings.ToLower(parsed.Hostname())
		if _, err2 := netip.ParseAddr(host); err2 == nil {
			if err := validateHTTPGetURLHost(context.Background(), host); err != nil {
				return input, nil, err
			}
		} else if _, blocked := blockedHostExact[host]; blocked ||
			strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
			strings.HasPrefix(host, "metadata.") {
			if err := validateHTTPGetURLHost(context.Background(), host); err != nil {
				return input, nil, err
			}
		}
	}
	return input, parsed, nil
}

func doHTTP(client *http.Client, req *http.Request, limit int64) ([]byte, string, error) {
	if client == nil {
		client = noRedirectClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, resp.Header.Get("Content-Type"), fmt.Errorf("http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > limit {
		body = body[:limit]
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func extractReadableText(body []byte, contentType string) string {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "html") || looksLikeHTML(body) {
		return htmlToText(body)
	}
	return string(body)
}

func looksLikeHTML(body []byte) bool {
	s := strings.TrimSpace(string(body))
	if len(s) < 5 {
		return false
	}
	lower := strings.ToLower(s[:min(64, len(s))])
	return strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html")
}

func htmlToText(body []byte) string {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return string(body)
	}
	var b strings.Builder
	skip := map[string]struct{}{"script": {}, "style": {}, "noscript": {}}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if _, drop := skip[strings.ToLower(n.Data)]; drop {
				return
			}
			switch strings.ToLower(n.Data) {
			case "p", "div", "br", "li", "h1", "h2", "h3", "h4", "tr":
				b.WriteByte('\n')
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return collapseSpace(b.String())
}

func collapseSpace(s string) string {
	var b strings.Builder
	nl := false
	space := false
	for _, r := range s {
		switch r {
		case '\n', '\r':
			if !nl {
				b.WriteByte('\n')
				nl = true
			}
			space = false
		case ' ', '\t':
			if !space && !nl {
				b.WriteByte(' ')
				space = true
			}
		default:
			b.WriteRune(r)
			nl = false
			space = false
		}
	}
	return strings.TrimSpace(b.String())
}

func truncateRunesHeadTail(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	marker := "\n[TRUNCATED]\n"
	keep := max - utf8.RuneCountInString(marker)
	if keep < 8 {
		return string(runes[:max])
	}
	head := keep / 2
	tail := keep - head
	return string(runes[:head]) + marker + string(runes[len(runes)-tail:])
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}
