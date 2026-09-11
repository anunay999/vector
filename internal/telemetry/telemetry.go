// Package telemetry records one JSONL line per routed request. It is intentionally
// append-only and best-effort: a telemetry failure must never fail a request.
package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record is a single request observation.
type Record struct {
	Time      time.Time `json:"time"`
	RequestID string    `json:"request_id"`
	Harness   string    `json:"harness"`
	// Session identifies the client session the request came from. Claude Code
	// sends X-Claude-Code-Session-Id and Codex sends session-id.
	Session string `json:"session,omitempty"`
	// Project is the working directory reported in the request's system prompt,
	// which maps a request to a checkout even when no session id is present.
	Project         string  `json:"project,omitempty"`
	Role            string  `json:"role,omitempty"`
	RequestedModel  string  `json:"requested_model"`
	RoutedModel     string  `json:"routed_model"`
	Provider        string  `json:"provider"`
	InboundShape    string  `json:"inbound_shape"`
	UpstreamShape   string  `json:"upstream_shape"`
	Translated      bool    `json:"translated"`
	Stream          bool    `json:"stream"`
	Status          int     `json:"status"`
	LatencyMS       int64   `json:"latency_ms"`
	InputTokens     int     `json:"input_tokens,omitempty"`
	OutputTokens    int     `json:"output_tokens,omitempty"`
	CacheReadTokens int     `json:"cache_read_tokens,omitempty"`
	EstCostUSD      float64 `json:"est_cost_usd,omitempty"`
	Reason          string  `json:"reason,omitempty"`
	Error           string  `json:"error,omitempty"`
	// Guard names a circuit breaker that fired on this request, for example
	// "thrash-warn" or "thrash-block".
	Guard string `json:"guard,omitempty"`
	// ToolSearch marks a request that used Claude Code's deferred tool loading.
	ToolSearch bool `json:"tool_search,omitempty"`
}

// Recorder appends records to daily JSONL files under dir.
type Recorder struct {
	dir     string
	enabled bool

	mu sync.Mutex
}

// New constructs a Recorder. If enabled is false, Record is a no-op.
func New(dir string, enabled bool) *Recorder {
	return &Recorder{dir: dir, enabled: enabled}
}

// Enabled reports whether recording is active.
func (r *Recorder) Enabled() bool { return r != nil && r.enabled }

// Record appends rec. Errors are returned but callers should treat them as
// non-fatal.
func (r *Recorder) Record(rec Record) error {
	if !r.Enabled() {
		return nil
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return err
	}
	name := filepath.Join(r.dir, "requests-"+rec.Time.Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// ReadSince reads all records with Time >= since from daily files. It is used by
// the spend command. Malformed lines are skipped.
func (r *Recorder) ReadSince(since time.Time) ([]Record, error) {
	if !r.Enabled() {
		return nil, nil
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(r.dir, e.Name())
		if !matchDay(path, since) {
			continue
		}
		recs, err := readFile(path)
		if err != nil {
			continue
		}
		for _, rec := range recs {
			if !rec.Time.Before(since) {
				out = append(out, rec)
			}
		}
	}
	return out, nil
}

func matchDay(path string, since time.Time) bool {
	base := filepath.Base(path)
	// requests-2006-01-02.jsonl
	if len(base) < len("requests-2006-01-02.jsonl") {
		return true
	}
	day := base[len("requests-") : len("requests-")+10]
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return true
	}
	// include the day of `since` and everything after
	return !t.Before(time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.UTC))
}

func readFile(path string) ([]Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
