// Package gateway exposes the loopback HTTP surface that harnesses point at. It
// speaks three wire shapes, resolves each request through the router, and either
// reverse-proxies it (same shape) or hands it to the translation layer.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anunay999/vector/internal/budget"
	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/llm"
	"github.com/anunay999/vector/internal/provider"
	"github.com/anunay999/vector/internal/registry"
	"github.com/anunay999/vector/internal/router"
	"github.com/anunay999/vector/internal/telemetry"
)

const (
	maxBodyBytes = 32 << 20 // 32 MiB
	captureBytes = 1 << 20  // per-response capture cap for usage extraction
)

// Server is the gateway HTTP handler.
type Server struct {
	cfg    *config.Config
	reg    *registry.Registry
	router *router.Router
	rec    *telemetry.Recorder
	gov    *budget.Governor
	client *provider.Client
	log    *slog.Logger
}

// New constructs a Server. The registry is derived from cfg.
func New(cfg *config.Config, rec *telemetry.Recorder, gov *budget.Governor, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	reg := registry.New(cfg)
	return &Server{
		cfg:    cfg,
		reg:    reg,
		router: router.New(cfg, reg),
		rec:    rec,
		gov:    gov,
		client: provider.NewClient(cfg.Fallback.TTFTTimeout),
		log:    log,
	}
}

// Handler returns the server as an http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.handleAnthropic)
	mux.HandleFunc("/v1/messages/count_tokens", s.handleCountTokens)
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAIChat)
	mux.HandleFunc("/v1/responses", s.handleOpenAIResponses)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/admin/status", loopbackOnly(s.handleStatus))
	return s.withRecovery(mux)
}

// ListenAndServe runs the gateway until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if addr == "" {
		return errors.New("gateway: empty listen address")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gateway: listen %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	s.log.Info("gateway listening", "addr", addr)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic in gateway", "err", rec, "path", r.URL.Path)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func loopbackOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleAnthropic(w http.ResponseWriter, r *http.Request) {
	s.proxy(w, r, llm.ShapeAnthropic, false)
}

func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	// count_tokens is a side channel: send it straight to the native provider
	// with the requested model, and never spend budget or a subagent slot.
	s.proxy(w, r, llm.ShapeAnthropic, true)
}

func (s *Server) handleOpenAIChat(w http.ResponseWriter, r *http.Request) {
	s.proxy(w, r, llm.ShapeOpenAIChat, false)
}

func (s *Server) handleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	s.proxy(w, r, llm.ShapeOpenAIResponses, false)
}

// envelope is the minimal routing projection of any inbound body.
type envelope struct {
	Model       string `json:"model"`
	Stream      bool   `json:"stream"`
	MaxTokens   int    `json:"max_tokens"`
	MaxOutput   int    `json:"max_output_tokens"`
	PromptChars int    `json:"-"`
}

func (e envelope) estimateOutput() int {
	if e.MaxOutput > e.MaxTokens {
		return e.MaxOutput
	}
	return e.MaxTokens
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request, shape llm.Shape, nativeOnly bool) {
	start := time.Now()
	requestID := newID()
	harness := detectHarness(r)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		s.writeError(w, shape, http.StatusBadRequest, "invalid request body")
		return
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		s.writeError(w, shape, http.StatusBadRequest, "request body is not valid JSON")
		return
	}
	if strings.TrimSpace(env.Model) == "" {
		s.writeError(w, shape, http.StatusBadRequest, "model is required")
		return
	}
	env.PromptChars = len(body)

	input := router.Input{
		Harness: harness,
		Shape:   shape,
		Model:   env.Model,
		Headers: flattenHeaders(r.Header),
	}

	var primary router.Decision
	if nativeOnly {
		primary, err = s.router.NativePassthrough(input)
	} else {
		primary, err = s.router.Route(input)
	}
	if err != nil {
		s.log.Warn("routing failed", "err", err, "model", env.Model)
		s.finish(w, shape, telemetry.Record{
			Time: start, RequestID: requestID, Harness: harness,
			RequestedModel: env.Model, InboundShape: string(shape), Stream: env.Stream,
		}, http.StatusBadGateway, err.Error())
		return
	}

	attempts := append([]router.Decision{primary}, primary.Candidates...)

	// Budget pre-flight (skipped for side-channel endpoints).
	if !nativeOnly {
		est := estimateCost(primary, env)
		switch bd := s.gov.Check(primary.Provider.ID, est); {
		case bd.Exceeded:
			s.finish(w, shape, telemetry.Record{
				Time: start, RequestID: requestID, Harness: harness, Role: primary.Role,
				RequestedModel: env.Model, RoutedModel: primary.UpstreamModel,
				Provider: primary.Provider.ID, InboundShape: string(shape), Stream: env.Stream,
				Reason: primary.Reason,
			}, http.StatusTooManyRequests, "vector: budget ceiling reached")
			return
		case bd.Queue:
			w.Header().Set("Retry-After", "5")
			s.finish(w, shape, telemetry.Record{
				Time: start, RequestID: requestID, Harness: harness, Role: primary.Role,
				RequestedModel: env.Model, RoutedModel: primary.UpstreamModel,
				Provider: primary.Provider.ID, InboundShape: string(shape), Stream: env.Stream,
				Reason: primary.Reason,
			}, http.StatusTooManyRequests, "vector: budget queue full")
			return
		case bd.Downgrade:
			// Try alternate candidates before the frontier choice.
			if len(primary.Candidates) > 0 {
				attempts = append(append([]router.Decision{}, primary.Candidates...), primary)
			}
		}
	}

	// Concurrency guard for subagent traffic.
	if !nativeOnly && primary.IsSubagent {
		release, aerr := s.gov.Acquire(harness)
		if aerr != nil {
			w.Header().Set("Retry-After", "2")
			s.finish(w, shape, telemetry.Record{
				Time: start, RequestID: requestID, Harness: harness, Role: primary.Role,
				RequestedModel: env.Model, RoutedModel: primary.UpstreamModel,
				Provider: primary.Provider.ID, InboundShape: string(shape), Stream: env.Stream,
				Reason: primary.Reason,
			}, http.StatusTooManyRequests, aerr.Error())
			return
		}
		defer release()
	}

	if nativeOnly {
		attempts = attempts[:1]
	}

	var lastErr error
	for i, dec := range attempts {
		if dec.Translate {
			lastErr = fmt.Errorf("translation from %s to %s is not implemented yet (provider %s)",
				shape, dec.UpstreamShape, dec.Provider.ID)
			continue
		}
		rec := telemetry.Record{
			Time: start, RequestID: requestID, Harness: harness, Role: dec.Role,
			RequestedModel: env.Model, RoutedModel: dec.UpstreamModel, Provider: dec.Provider.ID,
			InboundShape: string(shape), UpstreamShape: string(dec.UpstreamShape),
			Translated: dec.Translate, Stream: env.Stream, Reason: dec.Reason,
		}
		if i > 0 {
			rec.Reason = fmt.Sprintf("%s (fallback #%d)", rec.Reason, i)
		}

		outBody, rerr := rewriteModel(body, dec.UpstreamModel)
		if rerr != nil {
			lastErr = rerr
			continue
		}
		target := provider.Target{
			ID:      dec.Provider.ID,
			Type:    dec.Provider.Type,
			BaseURL: dec.BaseURL,
			APIKey:  dec.Provider.APIKey,
			Native:  dec.Provider.Native,
			Headers: dec.Provider.Headers,
		}
		upReq, berr := provider.BuildRequest(r.Context(), http.MethodPost, r.URL.Path, r.URL.RawQuery, outBody, r.Header, target)
		if berr != nil {
			lastErr = berr
			continue
		}

		resp, derr := s.client.Do(upReq)
		if derr != nil {
			lastErr = derr
			s.log.Warn("upstream error", "provider", dec.Provider.ID, "err", derr)
			rec.Status = http.StatusBadGateway
			rec.Error = derr.Error()
			rec.LatencyMS = time.Since(start).Milliseconds()
			_ = s.rec.Record(rec)
			continue
		}

		// Handle upstream errors. Retry the next candidate on transient
		// failures and on credit/quota exhaustion, so a harness whose native
		// plan is out of credits degrades to the cheap pool automatically.
		if resp.StatusCode >= 400 {
			peek, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			retryable := resp.StatusCode >= 500 ||
				resp.StatusCode == http.StatusTooManyRequests ||
				isCreditError(peek)
			if retryable && i < len(attempts)-1 {
				lastErr = fmt.Errorf("upstream %s returned %d", dec.Provider.ID, resp.StatusCode)
				rec.Status = resp.StatusCode
				rec.Error = lastErr.Error()
				rec.LatencyMS = time.Since(start).Milliseconds()
				_ = s.rec.Record(rec)
				s.log.Warn("falling back", "provider", dec.Provider.ID, "status", resp.StatusCode)
				continue
			}
			provider.CopyHeaders(w, resp.Header)
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(peek)
			rec.Status = resp.StatusCode
			rec.Error = truncate(string(peek), 300)
			rec.LatencyMS = time.Since(start).Milliseconds()
			_ = s.rec.Record(rec)
			return
		}

		capture := newCaptureWriter(w, captureBytes)
		_, copyErr := provider.CopyResponse(capture, resp)
		usage := extractUsage(resp.Header.Get("content-type"), capture.bytes())
		cost := costOf(dec, usage)
		if cost > 0 {
			s.gov.Commit(dec.Provider.ID, cost)
		}
		rec.Status = resp.StatusCode
		rec.InputTokens = usage.InputTokens
		rec.OutputTokens = usage.OutputTokens
		rec.CacheReadTokens = usage.CacheReadTokens
		rec.EstCostUSD = cost
		if copyErr != nil {
			rec.Error = copyErr.Error()
			s.log.Debug("stream copy ended", "err", copyErr)
		}
		rec.LatencyMS = time.Since(start).Milliseconds()
		_ = s.rec.Record(rec)
		return
	}

	msg := "upstream request failed"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	s.finish(w, shape, telemetry.Record{
		Time: start, RequestID: requestID, Harness: harness, Role: primary.Role,
		RequestedModel: env.Model, RoutedModel: primary.UpstreamModel, Provider: primary.Provider.ID,
		InboundShape: string(shape), Stream: env.Stream, Reason: primary.Reason,
	}, http.StatusBadGateway, msg)
}

// finish writes an error response and records it.
func (s *Server) finish(w http.ResponseWriter, shape llm.Shape, rec telemetry.Record, status int, msg string) {
	s.writeError(w, shape, status, msg)
	rec.Status = status
	rec.Error = msg
	if rec.LatencyMS == 0 {
		rec.LatencyMS = time.Since(rec.Time).Milliseconds()
	}
	_ = s.rec.Record(rec)
}

// creditMarkers indicate an upstream plan is out of credits or quota, which
// should trigger a fallback to the next candidate.
var creditMarkers = []string{
	"credit balance", "insufficient", "quota", "billing", "payment required",
	"not enough credits", "out of credits", "exceeded your current quota",
}

// isCreditError reports whether an error body indicates exhausted credits/quota.
func isCreditError(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	s := strings.ToLower(string(body))
	for _, m := range creditMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func estimateCost(d router.Decision, env envelope) float64 {
	if d.Price.In == 0 && d.Price.Out == 0 {
		return 0
	}
	promptTokens := float64(env.PromptChars) / 4.0
	return promptTokens*d.Price.In/1e6 + float64(env.estimateOutput())*d.Price.Out/1e6
}

func costOf(d router.Decision, u llm.Usage) float64 {
	return float64(u.InputTokens)*d.Price.In/1e6 + float64(u.OutputTokens)*d.Price.Out/1e6
}

// rewriteModel replaces the top-level "model" field, preserving all other fields.
func rewriteModel(body []byte, model string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	obj["model"] = raw
	return json.Marshal(obj)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"routing": s.cfg.RoutingEnabled,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	total, per := s.gov.Spend()
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"routing_enabled": s.cfg.RoutingEnabled,
		"spend_usd":       total,
		"per_provider":    per,
		"roles":           s.cfg.RoleNames(),
		"providers":       s.cfg.ProviderIDs(),
	})
}

// handleModels advertises virtual + native models for harness discovery. The
// payload carries both Anthropic and OpenAI discovery keys so it works for
// either client.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	type model struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		Object      string `json:"object"`
		DisplayName string `json:"display_name"`
		Name        string `json:"name"`
		Created     int64  `json:"created"`
		OwnedBy     string `json:"owned_by"`
	}
	var data []model
	for _, name := range s.cfg.RoleNames() {
		id := "vector-" + name
		data = append(data, model{ID: id, Type: "model", Object: "model", DisplayName: id, Name: id, OwnedBy: "vector"})
	}
	data = append(data, model{ID: "vector-auto", Type: "model", Object: "model", DisplayName: "vector-auto", Name: "vector-auto", OwnedBy: "vector"})
	for _, e := range s.reg.Entries() {
		data = append(data, model{ID: e.ID, Type: "model", Object: "model", DisplayName: e.ID, Name: e.ID, OwnedBy: e.ProviderID})
	}
	first, last := "", ""
	if len(data) > 0 {
		first = data[0].ID
		last = data[len(data)-1].ID
	}
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object":   "list",
		"data":     data,
		"has_more": false,
		"first_id": first,
		"last_id":  last,
	})
}

// writeError emits a shape-appropriate error body.
func (s *Server) writeError(w http.ResponseWriter, shape llm.Shape, status int, msg string) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	switch shape {
	case llm.ShapeAnthropic:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "api_error", "message": msg},
		})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": msg, "type": "vector_error"},
		})
	}
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

var knownHarnesses = map[string]bool{
	"claude-code": true,
	"codex":       true,
	"opencode":    true,
}

func detectHarness(r *http.Request) string {
	if h := r.Header.Get("X-Vector-Harness"); knownHarnesses[h] {
		return h
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	switch {
	case strings.Contains(ua, "claude"):
		return "claude-code"
	case strings.Contains(ua, "codex"):
		return "codex"
	case strings.Contains(ua, "opencode"):
		return "opencode"
	}
	if r.Header.Get("anthropic-version") != "" {
		return "claude-code"
	}
	return "unknown"
}
