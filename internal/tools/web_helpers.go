package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

type webSearchConfig struct{ backend, searxngURL, tavilyKey string }
type webSearchTool struct {
	cfg    webSearchConfig
	client *http.Client
}
type webSearchHit struct {
	Title   string
	URL     string
	Snippet string
}

const (
	ddgSearchURL    = "https://html.duckduckgo.com/html/"
	tavilySearchURL = "https://api.tavily.com/search"
	webSearchUA     = "Mozilla/5.0 (compatible; YunmengZe/0; +https://github.com/yyZe0122/yunmengze-agent)"
)

func (t *webSearchTool) search(ctx context.Context, query string, limit int) ([]webSearchHit, error) {
	switch t.cfg.backend {
	case "searxng":
		return t.searchSearxNG(ctx, query, limit)
	case "tavily":
		return t.searchTavily(ctx, query, limit)
	default:
		return t.searchDDG(ctx, query, limit)
	}
}

func (t *webSearchTool) searchDDG(ctx context.Context, query string, limit int) ([]webSearchHit, error) {
	form := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ddgSearchURL, strings.NewReader(form.Encode()))
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilySearchURL, bytes.NewReader(payload))
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
