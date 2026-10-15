package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDialStreamableJSON(t *testing.T) {
	t.Parallel()
	var sessionID string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var msg map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		method, _ := msg["method"].(string)
		id := msg["id"]
		mu.Lock()
		if sessionID == "" && method == "initialize" {
			sessionID = "sess-test-1"
		}
		sid := sessionID
		mu.Unlock()
		if sid != "" {
			w.Header().Set("Mcp-Session-Id", sid)
		}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "fake", "version": "0"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"tools": []map[string]any{
						{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}},
					},
				},
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			text, _ := args["text"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": text}},
					"isError": false,
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"error": map[string]any{"code": -32601, "message": "unknown"},
			})
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Dial(ctx, RemoteConfig{Name: "fake", URL: srv.URL, Mode: "http"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.transport != "http" {
		t.Fatalf("transport=%s", client.transport)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools=%+v", tools)
	}
	out, err := client.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "hi") {
		t.Fatalf("out=%s", out)
	}
}

func TestDialStreamableSSEBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", 405)
			return
		}
		var msg map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		method, _ := msg["method"].(string)
		id := msg["id"]
		if method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		var result any
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "sse", "version": "0"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "ping", "description": "p", "inputSchema": map[string]any{"type": "object"}}}}
		default:
			result = map[string]any{}
		}
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Dial(ctx, RemoteConfig{Name: "ssebody", URL: srv.URL, Mode: "http"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("tools=%+v", tools)
	}
}

func TestDialLegacySSE(t *testing.T) {
	t.Parallel()
	var messagePath string
	var mu sync.Mutex
	pending := make(map[float64]chan map[string]any)

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", 500)
			return
		}
		fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", messagePath)
		flusher.Flush()
		// Stream JSON-RPC responses for the life of the connection.
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				mu.Lock()
				for id, ch := range pending {
					select {
					case msg := <-ch:
						payload, _ := json.Marshal(msg)
						fmt.Fprintf(w, "data: %s\n\n", payload)
						flusher.Flush()
						delete(pending, id)
					default:
					}
				}
				mu.Unlock()
			}
		}
	})
	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		var msg map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		method, _ := msg["method"].(string)
		rawID := msg["id"]
		// notifications have no id
		if rawID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		id, _ := rawID.(float64)
		var result any
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "legacy", "version": "0"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "echo", "description": "e", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}
		default:
			result = map[string]any{}
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
		ch := make(chan map[string]any, 1)
		ch <- resp
		mu.Lock()
		pending[id] = ch
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	messagePath = srv.URL + "/message"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := Dial(ctx, RemoteConfig{Name: "legacy", URL: srv.URL + "/sse", Mode: "sse"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.transport != "sse" {
		t.Fatalf("transport=%s", client.transport)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools=%+v", tools)
	}
}

func TestDialRemoteAutoFallback(t *testing.T) {
	t.Parallel()
	// Streamable POST → 405; SSE works.
	var messagePath string
	var mu sync.Mutex
	pending := make(map[float64]chan map[string]any)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Error(w, "use sse", http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method", 405)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", messagePath)
		flusher.Flush()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				mu.Lock()
				for id, ch := range pending {
					select {
					case msg := <-ch:
						payload, _ := json.Marshal(msg)
						fmt.Fprintf(w, "data: %s\n\n", payload)
						flusher.Flush()
						delete(pending, id)
					default:
					}
				}
				mu.Unlock()
			}
		}
	})
	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		var msg map[string]any
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if msg["id"] == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		id, _ := msg["id"].(float64)
		method, _ := msg["method"].(string)
		var result any
		if method == "initialize" {
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "auto", "version": "0"}}
		} else {
			result = map[string]any{"tools": []any{}}
		}
		ch := make(chan map[string]any, 1)
		ch <- map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
		mu.Lock()
		pending[id] = ch
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	messagePath = srv.URL + "/message"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := Dial(ctx, RemoteConfig{Name: "auto", URL: srv.URL + "/mcp", Mode: "remote"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.transport != "sse" {
		t.Fatalf("want sse fallback, got %s", client.transport)
	}
}
