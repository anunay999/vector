// Package gateway exposes the loopback HTTP surface that harnesses point at. It
// speaks three wire shapes, resolves each request through the router, and either
// reverse-proxies it (same shape) or hands it to the translation layer.
package gateway

import (
	"bytes"
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
	"sync/atomic"
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

// runtime is the swappable set of routing components. Reload builds a new one
// and installs it atomically, so in-flight requests keep their snapshot.
type runtime struct {
	cfg    *config.Config
	reg    *registry.Registry
	router *router.Router
	rec    *telemetry.Recorder
	client *provider.Client
}

// Server is the gateway HTTP handler.
type Server struct {
	rt     atomic.Pointer[runtime]
	gov    *budget.Governor
	thrash *thrashGuard
	log    *slog.Logger
}

func buildRuntime(cfg *config.Config, rec *telemetry.Recorder) *runtime {
	reg := registry.New(cfg)
	return &runtime{
		cfg:    cfg,
		reg:    reg,
		router: router.New(cfg, reg),
		rec:    rec,
		client: provider.NewClient(cfg.Fallback.TTFTTimeout),
	}
}

// New constructs a Server. The registry is derived from cfg.
func New(cfg *config.Config, rec *telemetry.Recorder, gov *budget.Governor, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{gov: gov, thrash: newThrashGuard(cfg.Guard.Thrash), log: log}
	s.rt.Store(buildRuntime(cfg, rec))
	return s
}

func (s *Server) current() *runtime { return s.rt.Load() }

// Apply installs a new configuration without dropping the gateway.
func (s *Server) Apply(cfg *config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	rec := telemetry.New(cfg.TelemetryDir(), cfg.Telemetry.Enabled)
	s.rt.Store(buildRuntime(cfg, rec))
	s.gov.Reconfigure(cfg.Budget.DailyUSD, cfg.Budget.PerProvider, cfg.Budget.OnBreach, cfg.Budget.MaxConcurrentSubagentsPerHarness)
	s.thrash.reconfigure(cfg.Guard.Thrash)
	s.log.Info("config reloaded", "routing", cfg.RoutingEnabled,
		"providers", len(cfg.Providers), "models", len(cfg.Models), "roles", len(cfg.Roles))
	return nil
}

// Reload re-reads the config from disk and applies it.
func (s *Server) Reload() error {
	path := s.current().cfg.Path()
	if path == "" {
		return errors.New("gateway: cannot reload, config has no path")
	}
	cfg, err := config.LoadFrom(path)
	if err != nil {
		return err
	}
	return s.Apply(cfg)
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
	rt := s.rt.Load()
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

	session := sessionID(r)
	project := workingDir(body)
	subagent := isSubagentRequest(r, body)

	input := router.Input{
		Harness:    harness,
		Shape:      shape,
		Model:      env.Model,
		IsSubagent: subagent,
		Headers:    flattenHeaders(r.Header),
	}
	base := telemetry.Record{
		Time: start, RequestID: requestID, Harness: harness, Session: session, Project: project,
		RequestedModel: env.Model, InboundShape: string(shape), Stream: env.Stream,
		ToolSearch: hasToolSearch(body),
	}

	var primary router.Decision
	if nativeOnly {
		primary, err = rt.router.NativePassthrough(input)
	} else {
		primary, err = rt.router.Route(input)
	}
	if err != nil {
		s.log.Warn("routing failed", "err", err, "model", env.Model)
		s.finish(rt.rec, w, shape, base, http.StatusBadGateway, err.Error())
		return
	}

	attempts := []router.Decision{primary}

	// Context-thrash breaker (skipped for side-channel endpoints). A tripped
	// session is either blocked with an explanation or let through with a
	// warning header, per guard.thrash.action.
	if !nativeOnly {
		if v := s.thrash.check(session); v.Tripped {
			if v.Block {
				w.Header().Set("Retry-After", "60")
				rec := base
				rec.Role, rec.RoutedModel, rec.Provider = primary.Role, primary.UpstreamModel, primary.Provider.ID
				rec.Reason, rec.Guard = primary.Reason, "thrash-block"
				s.finish(rt.rec, w, shape, rec, http.StatusTooManyRequests, v.Detail)
				return
			}
			w.Header().Set("X-Vector-Warning", v.Detail)
			base.Guard = "thrash-warn"
		}
	}

	// Budget pre-flight (skipped for side-channel endpoints).
	if !nativeOnly {
		est := estimateCost(primary, env)
		switch bd := s.gov.Check(primary.Provider.ID, est); {
		case bd.Exceeded:
			rec := base
			rec.Role, rec.RoutedModel, rec.Provider = primary.Role, primary.UpstreamModel, primary.Provider.ID
			rec.Reason = primary.Reason
			s.finish(rt.rec, w, shape, rec, http.StatusTooManyRequests, "vector: budget ceiling reached")
			return
		case bd.Queue:
			w.Header().Set("Retry-After", "5")
			rec := base
			rec.Role, rec.RoutedModel, rec.Provider = primary.Role, primary.UpstreamModel, primary.Provider.ID
			rec.Reason = primary.Reason
			s.finish(rt.rec, w, shape, rec, http.StatusTooManyRequests, "vector: budget queue full")
			return
		case bd.Downgrade:
			// No fallback pool in the two-mode model: budget still gates spend,
			// but there is no cheaper candidate to reorder ahead of the choice.
		}
	}

	// Concurrency guard for subagent traffic.
	if !nativeOnly && primary.IsSubagent {
		release, aerr := s.gov.Acquire(harness)
		if aerr != nil {
			w.Header().Set("Retry-After", "2")
			rec := base
			rec.Role, rec.RoutedModel, rec.Provider = primary.Role, primary.UpstreamModel, primary.Provider.ID
			rec.Reason = primary.Reason
			s.finish(rt.rec, w, shape, rec, http.StatusTooManyRequests, aerr.Error())
			return
		}
		defer release()
	}

	var lastErr error
	translationBlocked := false
	for i, dec := range attempts {
		if dec.Translate {
			lastErr = fmt.Errorf("translation from %s to %s is not implemented yet (provider %s)",
				shape, dec.UpstreamShape, dec.Provider.ID)
			translationBlocked = true
			continue
		}
		translationBlocked = false
		rec := base
		rec.Role, rec.RoutedModel, rec.Provider = dec.Role, dec.UpstreamModel, dec.Provider.ID
		rec.UpstreamShape = string(dec.UpstreamShape)
		rec.Translated = dec.Translate
		rec.Reason = dec.Reason
		if i > 0 {
			rec.Reason = fmt.Sprintf("%s (fallback #%d)", rec.Reason, i)
		}

		keepExtras := dec.Provider.Type == config.ProviderAnthropic
		outBody, rerr := prepareBody(body, dec.UpstreamModel, keepExtras)
		if rerr != nil {
			lastErr = rerr
			continue
		}
		hdr := upstreamHeaders(r.Header, keepExtras)
		target := provider.Target{
			ID:      dec.Provider.ID,
			Type:    dec.Provider.Type,
			BaseURL: dec.BaseURL,
			APIKey:  dec.Provider.APIKey,
			Native:  dec.Provider.Native,
			Headers: dec.Provider.Headers,
		}
		upReq, berr := provider.BuildRequest(r.Context(), http.MethodPost, r.URL.Path, r.URL.RawQuery, outBody, hdr, target)
		if berr != nil {
			lastErr = berr
			continue
		}

		resp, derr := rt.client.Do(upReq)
		if derr != nil {
			lastErr = derr
			s.log.Warn("upstream error", "provider", dec.Provider.ID, "err", derr)
			rec.Status = http.StatusBadGateway
			rec.Error = derr.Error()
			rec.LatencyMS = time.Since(start).Milliseconds()
			_ = rt.rec.Record(rec)
			continue
		}

		// Upstream errors surface to the harness unchanged. There is no
		// fallback pool: a request is either served as routed or it fails, and
		// the harness (which owns its own retry) decides what to do next.
		if resp.StatusCode >= 400 {
			peek, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			setVectorHeaders(w, requestID, dec, env.Model, rec.Reason)
			provider.CopyHeaders(w, resp.Header)
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(peek)
			rec.Status = resp.StatusCode
			rec.Error = truncate(string(peek), 300)
			rec.LatencyMS = time.Since(start).Milliseconds()
			_ = rt.rec.Record(rec)
			return
		}

		setVectorHeaders(w, requestID, dec, env.Model, rec.Reason)
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
		if !nativeOnly {
			if tripped, n := s.thrash.observe(session, usage); tripped {
				rec.Guard = "thrash-trip"
				s.log.Warn("context thrash detected", "session", session, "harness", harness,
					"cold_rebuilds", n, "prompt_tokens", usage.InputTokens+usage.CacheWriteTokens,
					"action", rt.cfg.Guard.Thrash.Action)
			}
		}
		if copyErr != nil {
			rec.Error = copyErr.Error()
			s.log.Debug("stream copy ended", "err", copyErr)
		}
		rec.LatencyMS = time.Since(start).Milliseconds()
		_ = rt.rec.Record(rec)
		return
	}

	msg := "upstream request failed"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	status := http.StatusBadGateway
	if translationBlocked {
		status = http.StatusNotImplemented
	}
	rec := base
	rec.Role, rec.RoutedModel, rec.Provider = primary.Role, primary.UpstreamModel, primary.Provider.ID
	rec.Reason = primary.Reason
	s.finish(rt.rec, w, shape, rec, status, msg)
}

// finish writes an error response and records it through the request's own
// recorder, so a mid-flight config reload cannot split one request's records
// across two recorders.
func (s *Server) finish(recorder *telemetry.Recorder, w http.ResponseWriter, shape llm.Shape, rec telemetry.Record, status int, msg string) {
	if rec.RequestID != "" {
		w.Header().Set("X-Vector-Request-Id", rec.RequestID)
	}
	s.writeError(w, shape, status, msg)
	rec.Status = status
	rec.Error = msg
	if rec.LatencyMS == 0 {
		rec.LatencyMS = time.Since(rec.Time).Milliseconds()
	}
	_ = recorder.Record(rec)
}

// setVectorHeaders emits the request id on every response and, when the served
// model differs from the one requested, the served-by triple, so a harness log
// can reach `vector explain <id>` and a swapped model is never silent.
func setVectorHeaders(w http.ResponseWriter, requestID string, dec router.Decision, requested, reason string) {
	if requestID != "" {
		w.Header().Set("X-Vector-Request-Id", requestID)
	}
	if dec.UpstreamModel != "" && dec.UpstreamModel != requested {
		w.Header().Set("X-Vector-Served-By", dec.Provider.ID+"/"+dec.UpstreamModel+"; reason="+reason)
	}
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
	return float64(u.InputTokens)*d.Price.In/1e6 +
		float64(u.OutputTokens)*d.Price.Out/1e6 +
		float64(u.CacheReadTokens)*d.Price.CacheRead/1e6 +
		float64(u.CacheWriteTokens)*d.Price.CacheWrite/1e6
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	rt := s.rt.Load()
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"routing": rt.cfg.RoutingEnabled,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	rt := s.rt.Load()
	total, per := s.gov.Spend()
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"routing_enabled": rt.cfg.RoutingEnabled,
		"spend_usd":       total,
		"per_provider":    per,
		"roles":           rt.cfg.RoleNames(),
		"providers":       rt.cfg.ProviderIDs(),
	})
}

// handleModels advertises virtual, registry, and native models for harness discovery. The
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
	for _, name := range s.rt.Load().cfg.RoleNames() {
		id := "vector-" + name
		data = append(data, model{ID: id, Type: "model", Object: "model", DisplayName: id, Name: id, OwnedBy: "vector"})
	}
	for _, e := range s.rt.Load().reg.Entries() {
		data = append(data, model{ID: e.ID, Type: "model", Object: "model", DisplayName: e.ID, Name: e.ID, OwnedBy: e.ProviderID})
	}
	// Native providers serve any real model id by passthrough; advertise at
	// least their default so a harness that discovers models through the
	// gateway (Claude Code with CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY) sees
	// its own frontier model listed rather than a gateway with no Claude models.
	for _, p := range s.rt.Load().cfg.Providers {
		if p.Native && p.DefaultModel != "" {
			data = append(data, model{ID: p.DefaultModel, Type: "model", Object: "model", DisplayName: p.DefaultModel, Name: p.DefaultModel, OwnedBy: p.ID})
		}
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

// sessionID identifies the originating client session from headers. Claude Code
// sends X-Claude-Code-Session-Id and Codex sends session-id.
func sessionID(r *http.Request) string {
	for _, h := range []string{"X-Claude-Code-Session-Id", "Session-Id"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	return ""
}

// workingDir extracts "Working directory: <path>" from the request body's system
// prompt, which Claude Code and Codex both include.
func workingDir(body []byte) string {
	const marker = "Working directory: "
	i := bytes.Index(body, []byte(marker))
	if i < 0 {
		return ""
	}
	rest := body[i+len(marker):]
	if end := bytes.IndexAny(rest, "\n\r\\\""); end >= 0 {
		rest = rest[:end]
	}
	if len(rest) > 300 {
		rest = rest[:300]
	}
	return strings.TrimSpace(string(rest))
}

// isSubagentRequest reports whether a request was spawned by a harness subagent
// (the Task/Agent tool) rather than the main thread. Detection is header-first
// and scoped: it reads only the harness's own agent header and its billing
// system block, never user or assistant content (telemetry stays metadata-only).
func isSubagentRequest(r *http.Request, body []byte) bool {
	if strings.TrimSpace(r.Header.Get("X-Claude-Code-Agent-Id")) != "" {
		return true
	}
	if codexAgentRequest(r.Header.Get("X-Codex-Turn-Metadata")) {
		return true
	}
	return ccSystemBlockMarksSubagent(body)
}

// codexAgentRequest reports whether Codex's turn metadata names a spawned agent
// rather than the root conversation. The metadata is JSON, so parse it instead
// of substring-matching, which misreads `"agent_name": "/root"` when spaced.
func codexAgentRequest(meta string) bool {
	if strings.TrimSpace(meta) == "" {
		return false
	}
	var m struct {
		AgentName string `json:"agent_name"`
	}
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return false
	}
	name := strings.TrimSpace(m.AgentName)
	return name != "" && name != "/root"
}

// ccSystemBlockMarksSubagent looks for Claude Code's subagent marker only inside
// the top-level "system" field (a string or an array of text blocks). Scanning
// the whole body false-positives whenever a tool result contains the literal —
// for example when the session reads vector's own source.
func ccSystemBlockMarksSubagent(body []byte) bool {
	var env struct {
		System json.RawMessage `json:"system"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.System) == 0 {
		return false
	}
	return bytes.Contains(env.System, []byte("cc_is_subagent=true"))
}
