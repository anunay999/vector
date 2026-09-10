// Package provider builds and performs upstream requests. It owns URL joining,
// credential selection (configured key vs inbound plan credential), header
// hygiene, and timeouts. It contains no policy: the router decides, the provider
// package delivers.
package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Target describes where and how to reach an upstream.
type Target struct {
	ID      string
	Type    string
	BaseURL string
	APIKey  string
	Native  bool
	Headers map[string]string
}

// versionHeaders are forwarded so upstreams see the protocol version negotiated
// by the harness.
var versionHeaders = []string{
	"content-type",
	"accept",
	"anthropic-version",
	"anthropic-beta",
	"anthropic-dangerous-direct-browser-access",
	"openai-beta",
	"openai-organization",
	"openai-project",
}

// BuildRequest constructs the upstream request for a raw (passthrough) body.
func BuildRequest(ctx context.Context, method, inboundPath, rawQuery string, body []byte, inbound http.Header, t Target) (*http.Request, error) {
	url := JoinURL(t.BaseURL, inboundPath)
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("provider: build request: %w", err)
	}
	req.ContentLength = int64(len(body))

	for _, h := range versionHeaders {
		if v := inbound.Values(h); len(v) > 0 {
			for _, one := range v {
				req.Header.Add(h, one)
			}
		}
	}
	if req.Header.Get("content-type") == "" {
		req.Header.Set("content-type", "application/json")
	}

	applyAuth(req, inbound, t)
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

func applyAuth(req *http.Request, inbound http.Header, t Target) {
	if t.Native {
		// Reuse the caller's plan credential exactly as presented.
		if v := inbound.Get("Authorization"); v != "" {
			req.Header.Set("Authorization", v)
		}
		if v := inbound.Get("X-Api-Key"); v != "" {
			req.Header.Set("X-Api-Key", v)
		}
		return
	}
	if t.APIKey == "" {
		return
	}
	if t.Type == "anthropic" {
		req.Header.Set("X-Api-Key", t.APIKey)
		return
	}
	req.Header.Set("Authorization", "Bearer "+t.APIKey)
}

// JoinURL joins a base URL and an inbound path without duplicating a version
// segment. For example base "https://openrouter.ai/api/v1" plus path
// "/v1/messages" yields "https://openrouter.ai/api/v1/messages".
func JoinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if path == "" {
		return base
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for _, v := range []string{"/api/v1", "/v1"} {
		if strings.HasSuffix(base, v) && strings.HasPrefix(path, v) {
			return base + strings.TrimPrefix(path, v)
		}
	}
	return base + path
}

// Client wraps an http.Client with a streaming-friendly transport.
type Client struct {
	hc *http.Client
}

// NewClient returns a client whose transport times out on first byte
// (ResponseHeaderTimeout) but not on the overall streamed body.
func NewClient(ttft time.Duration) *Client {
	if ttft <= 0 {
		ttft = 30 * time.Second
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: ttft,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &Client{hc: &http.Client{Transport: tr}}
}

// Do performs the upstream request.
func (c *Client) Do(req *http.Request) (*http.Response, error) { return c.hc.Do(req) }

// CopyResponse streams an upstream response to the client. It copies status and
// a safe subset of headers and returns once the body is fully copied.
func CopyResponse(w http.ResponseWriter, resp *http.Response) (int64, error) {
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func isHopByHop(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "content-length",
		"content-encoding":
		return true
	}
	return false
}
