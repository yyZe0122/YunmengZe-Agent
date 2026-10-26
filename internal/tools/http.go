package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

func newWebClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

var errResponseTooLarge = errors.New("HTTP response exceeds size limit")

func httpRequest(ctx context.Context, address string) (*http.Request, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("absolute HTTP(S) URL without userinfo is required")
	}
	return http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
}

func doHTTP(client *http.Client, req *http.Request, limit int64) ([]byte, string, error) {
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
		return body[:limit], resp.Header.Get("Content-Type"), fmt.Errorf("%w (%d bytes)", errResponseTooLarge, limit)
	}
	return body, resp.Header.Get("Content-Type"), nil
}
