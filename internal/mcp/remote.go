package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RemoteConfig dials one remote MCP server (Streamable HTTP and/or legacy SSE).
type RemoteConfig struct {
	Name    string
	URL     string
	Headers map[string]string
	// Mode: "http" (streamable only), "sse" (legacy only), "remote"/"" (auto).
	Mode string
}

// RemoteClient is one connected remote MCP server.
type RemoteClient struct {
	name      string
	transport string // "http" or "sse"
	baseURL   string // streamable endpoint or legacy message POST base
	headers   map[string]string
	http      *http.Client
	mu        sync.Mutex
	nextID    atomic.Int64
	closed    atomic.Bool
	sessionID string

	// legacy SSE
	legacy     bool
	messageURL string
	sseCancel  context.CancelFunc
	sseDone    chan struct{}
	pending    map[int64]chan map[string]any
	pendingMu  sync.Mutex
}

// Dial connects to a remote MCP server and completes initialize + initialized.
func Dial(ctx context.Context, config RemoteConfig) (*RemoteClient, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		return nil, errors.New("mcp server name is required")
	}
	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return nil, errors.New("mcp url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid mcp url")
	}
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	if mode == "" {
		mode = "remote"
	}
	headers := make(map[string]string, len(config.Headers))
	for k, v := range config.Headers {
		headers[k] = v
	}
	client := &http.Client{
		Timeout: 0, // per-request via context
		// Avoid hanging forever on redirects with body.
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}

	switch mode {
	case "http":
		return dialStreamable(ctx, name, rawURL, headers, client)
	case "sse":
		return dialLegacySSE(ctx, name, rawURL, headers, client)
	case "remote":
		rc, err := dialStreamable(ctx, name, rawURL, headers, client)
		if err == nil {
			return rc, nil
		}
		// Fallback only on clear "not streamable" signals.
		if !isStreamableUnsupported(err) {
			return nil, err
		}
		return dialLegacySSE(ctx, name, rawURL, headers, client)
	default:
		return nil, fmt.Errorf("unsupported remote mode %q", mode)
	}
}

func isStreamableUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "streamable unsupported") ||
		strings.Contains(msg, "405") ||
		strings.Contains(msg, "404") ||
		strings.Contains(msg, "status 400") ||
		strings.Contains(msg, "status 415")
}

func dialStreamable(ctx context.Context, name, endpoint string, headers map[string]string, httpClient *http.Client) (*RemoteClient, error) {
	rc := &RemoteClient{
		name: name, transport: "http", baseURL: endpoint, headers: headers, http: httpClient,
	}
	if err := rc.initializeStreamable(ctx); err != nil {
		_ = rc.Close()
		return nil, err
	}
	return rc, nil
}

func dialLegacySSE(ctx context.Context, name, sseURL string, headers map[string]string, httpClient *http.Client) (*RemoteClient, error) {
	rc := &RemoteClient{
		name: name, transport: "sse", baseURL: sseURL, headers: headers, http: httpClient,
		legacy: true, pending: make(map[int64]chan map[string]any),
	}
	if err := rc.startLegacySSE(ctx); err != nil {
		_ = rc.Close()
		return nil, err
	}
	if err := rc.initializeLegacy(ctx); err != nil {
		_ = rc.Close()
		return nil, err
	}
	return rc, nil
}

// ensure Session interface
var _ Session = (*RemoteClient)(nil)
var _ Session = (*Client)(nil)

// Close ends the remote session.
func (c *RemoteClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.legacy {
		if c.sseCancel != nil {
			c.sseCancel()
		}
		if c.sseDone != nil {
			select {
			case <-c.sseDone:
			case <-time.After(2 * time.Second):
			}
		}
		c.pendingMu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()
		return nil
	}
	// Streamable: optional DELETE session
	if c.sessionID != "" && c.baseURL != "" {
		reqCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodDelete, c.baseURL, nil)
		if err == nil {
			c.applyHeaders(req)
			req.Header.Set("Mcp-Session-Id", c.sessionID)
			resp, err := c.http.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}
	}
	return nil
}

// ListTools calls tools/list.
func (c *RemoteClient) ListTools(ctx context.Context) ([]ToolDesc, error) {
	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return parseToolList(result), nil
}

// CallTool invokes tools/call.
func (c *RemoteClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var args any
	if err := json.Unmarshal(arguments, &args); err != nil {
		return nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	var result map[string]any
	if err := c.call(ctx, "tools/call", map[string]any{
		"name": name, "arguments": args,
	}, &result); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (c *RemoteClient) initializeStreamable(ctx context.Context) error {
	var result map[string]any
	if err := c.callStreamable(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "yunmengze", "version": "0"},
	}, &result); err != nil {
		return err
	}
	return c.notifyStreamable(ctx, "notifications/initialized", map[string]any{})
}

func (c *RemoteClient) initializeLegacy(ctx context.Context) error {
	var result map[string]any
	if err := c.callLegacy(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "yunmengze", "version": "0"},
	}, &result); err != nil {
		return err
	}
	return c.notifyLegacy(ctx, "notifications/initialized", map[string]any{})
}

func (c *RemoteClient) call(ctx context.Context, method string, params any, result any) error {
	if c.legacy {
		return c.callLegacy(ctx, method, params, result)
	}
	return c.callStreamable(ctx, method, params, result)
}

func (c *RemoteClient) applyHeaders(req *http.Request) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
}

func (c *RemoteClient) callStreamable(ctx context.Context, method string, params any, result any) error {
	if c.closed.Load() {
		return errors.New("mcp client closed")
	}
	id := c.nextID.Add(1)
	reqBody := map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mcp %s %s: %w", c.name, method, err)
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if method == "initialize" && (resp.StatusCode == 404 || resp.StatusCode == 405 || resp.StatusCode == 400 || resp.StatusCode == 415) {
			return fmt.Errorf("streamable unsupported: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
		}
		return fmt.Errorf("mcp %s %s: status %d: %s", c.name, method, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "text/event-stream"):
		msg, err := readSSEJSONRPCResponse(resp.Body, id)
		if err != nil {
			return fmt.Errorf("mcp %s %s: %w", c.name, method, err)
		}
		return decodeRPCResult(c.name, method, msg, result)
	case strings.Contains(ct, "application/json") || ct == "" || strings.Contains(ct, "json"):
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			if result == nil {
				return nil
			}
			return fmt.Errorf("mcp %s %s: empty response", c.name, method)
		}
		var msg map[string]any
		if err := json.Unmarshal(raw, &msg); err != nil {
			return fmt.Errorf("mcp %s %s: decode: %w", c.name, method, err)
		}
		return decodeRPCResult(c.name, method, msg, result)
	default:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		if method == "initialize" {
			return fmt.Errorf("streamable unsupported: content-type %q: %s", ct, strings.TrimSpace(string(snippet)))
		}
		return fmt.Errorf("mcp %s %s: unexpected content-type %q", c.name, method, ct)
	}
}

func (c *RemoteClient) notifyStreamable(ctx context.Context, method string, params any) error {
	if c.closed.Load() {
		return errors.New("mcp client closed")
	}
	reqBody := map[string]any{
		"jsonrpc": "2.0", "method": method, "params": params,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 202 Accepted or 2xx with empty body is fine.
	if resp.StatusCode == http.StatusAccepted || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return fmt.Errorf("mcp %s %s: status %d: %s", c.name, method, resp.StatusCode, strings.TrimSpace(string(snippet)))
}

func decodeRPCResult(server, method string, msg map[string]any, result any) error {
	if errObj, ok := msg["error"]; ok && errObj != nil {
		return fmt.Errorf("mcp %s %s: %v", server, method, errObj)
	}
	if result == nil {
		return nil
	}
	payload, err := json.Marshal(msg["result"])
	if err != nil {
		return err
	}
	if string(payload) == "null" {
		return nil
	}
	return json.Unmarshal(payload, result)
}

// readSSEJSONRPCResponse reads an SSE stream until a JSON-RPC response with matching id.
func readSSEJSONRPCResponse(r io.Reader, wantID int64) (map[string]any, error) {
	sc := bufio.NewScanner(r)
	// Allow large tool results.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 4<<20)
	var dataLines []string
	flush := func() (map[string]any, bool, error) {
		if len(dataLines) == 0 {
			return nil, false, nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		data = strings.TrimSpace(data)
		if data == "" {
			return nil, false, nil
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return nil, false, nil // ignore non-JSON events
		}
		rawID, hasID := msg["id"]
		if !hasID {
			return nil, false, nil
		}
		if !idEqual(rawID, wantID) {
			return nil, false, nil
		}
		return msg, true, nil
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if msg, ok, err := flush(); err != nil {
				return nil, err
			} else if ok {
				return msg, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		// Ignore event:/id:/retry:
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if msg, ok, err := flush(); err != nil {
		return nil, err
	} else if ok {
		return msg, nil
	}
	return nil, errors.New("sse stream ended without response")
}

// --- legacy HTTP+SSE (2024-11-05) ---

func (c *RemoteClient) startLegacySSE(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return err
	}
	c.applyHeaders(req)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mcp %s sse connect: %w", c.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		_ = resp.Body.Close()
		return fmt.Errorf("mcp %s sse connect: status %d: %s", c.name, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	// Single reader: wait for endpoint, then dispatch JSON-RPC responses.
	endpointCh := make(chan string, 1)
	errCh := make(chan error, 1)
	sseCtx, cancel := context.WithCancel(context.Background())
	c.sseCancel = cancel
	c.sseDone = make(chan struct{})
	go c.legacySSELoop(sseCtx, resp.Body, endpointCh, errCh)

	select {
	case <-ctx.Done():
		cancel()
		_ = resp.Body.Close()
		return ctx.Err()
	case err := <-errCh:
		cancel()
		return err
	case <-time.After(15 * time.Second):
		cancel()
		return errors.New("mcp sse: timeout waiting for endpoint event")
	case ep := <-endpointCh:
		msgURL, err := resolveMessageURL(c.baseURL, ep)
		if err != nil {
			cancel()
			return err
		}
		c.messageURL = msgURL
	}
	return nil
}

func resolveMessageURL(sseBase, endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", errors.New("empty endpoint")
	}
	base, err := url.Parse(sseBase)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func (c *RemoteClient) legacySSELoop(ctx context.Context, body io.ReadCloser, endpointCh chan<- string, errCh chan<- error) {
	defer close(c.sseDone)
	defer body.Close()
	sc := bufio.NewScanner(body)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 4<<20)
	var eventType string
	var dataLines []string
	endpointSent := false
	flush := func() {
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		et := eventType
		eventType = ""
		dataLines = nil
		if data == "" {
			return
		}
		if !endpointSent && (et == "endpoint" || strings.HasPrefix(data, "/") || strings.HasPrefix(data, "http")) {
			// Prefer explicit endpoint event; also accept bare path/URL data before any JSON-RPC.
			if et == "endpoint" || !strings.HasPrefix(data, "{") {
				endpointSent = true
				select {
				case endpointCh <- data:
				default:
				}
				return
			}
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return
		}
		rawID, hasID := msg["id"]
		if !hasID {
			return
		}
		c.pendingMu.Lock()
		for id, ch := range c.pending {
			if idEqual(rawID, id) {
				select {
				case ch <- msg:
				default:
				}
				break
			}
		}
		c.pendingMu.Unlock()
	}
	for {
		if ctx.Err() != nil {
			return
		}
		if !sc.Scan() {
			if !endpointSent {
				select {
				case errCh <- errors.New("sse stream ended without endpoint event"):
				default:
				}
			}
			return
		}
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}

func (c *RemoteClient) callLegacy(ctx context.Context, method string, params any, result any) error {
	if c.closed.Load() {
		return errors.New("mcp client closed")
	}
	if c.messageURL == "" {
		return errors.New("mcp sse: message endpoint not ready")
	}
	id := c.nextID.Add(1)
	reqBody := map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	respCh := make(chan map[string]any, 1)
	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.messageURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	// Serialize POSTs; responses arrive on SSE.
	c.mu.Lock()
	resp, err := c.http.Do(req)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("mcp %s %s: %w", c.name, method, err)
	}
	// Some servers return JSON on POST; others return 202 and reply on SSE.
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	status := resp.StatusCode
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	_ = resp.Body.Close()
	c.mu.Unlock()

	if status < 200 || status >= 300 {
		return fmt.Errorf("mcp %s %s: status %d: %s", c.name, method, status, strings.TrimSpace(string(raw)))
	}

	if strings.Contains(ct, "application/json") && len(bytes.TrimSpace(raw)) > 0 {
		var msg map[string]any
		if err := json.Unmarshal(raw, &msg); err == nil {
			if _, hasID := msg["id"]; hasID {
				return decodeRPCResult(c.name, method, msg, result)
			}
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case msg, ok := <-respCh:
		if !ok || msg == nil {
			return errors.New("mcp sse: connection closed")
		}
		return decodeRPCResult(c.name, method, msg, result)
	}
}

func (c *RemoteClient) notifyLegacy(ctx context.Context, method string, params any) error {
	if c.messageURL == "" {
		return errors.New("mcp sse: message endpoint not ready")
	}
	reqBody := map[string]any{
		"jsonrpc": "2.0", "method": method, "params": params,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.messageURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	c.mu.Lock()
	defer c.mu.Unlock()
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return fmt.Errorf("mcp %s %s: status %d: %s", c.name, method, resp.StatusCode, strings.TrimSpace(string(snippet)))
}
