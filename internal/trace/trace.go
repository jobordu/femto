// Package trace emits one self-describing JSON line per agent run, appended and
// fsync'd so a crash mid-sweep keeps prior traces (append-only, worst case a partial
// last line). The record carries everything a later DB backfill needs — model, task,
// outcome, cost proxy, transcript — matching the flynn agent-trace shape.
package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/jobordu/femto/internal/agent"
)

// Record is one run's trace line.
type Record struct {
	TS         float64          `json:"ts"`
	RunID      string           `json:"run_id,omitempty"`
	TaskID     string           `json:"task_id,omitempty"`
	Benchmark  string           `json:"benchmark,omitempty"`
	Category   string           `json:"category,omitempty"`
	Model      string           `json:"model"`
	Native     bool             `json:"native"`
	Temp       float64          `json:"temperature"`
	Prompt     string           `json:"prompt,omitempty"`
	Solved     bool             `json:"solved"`
	Final      string           `json:"final"`
	Steps      int              `json:"steps"`
	StopReason string           `json:"stop_reason"`
	LLMCalls   int              `json:"llm_calls"`
	Transcript []map[string]any `json:"transcript,omitempty"`
}

// FromResult builds a Record from an AgentResult plus the run metadata. now is the
// unix timestamp (injected so it's testable / not clock-coupled here).
func FromResult(res agent.AgentResult, meta Record, now float64) Record {
	meta.TS = now
	meta.Solved = res.Solved
	meta.Final = res.Final
	meta.Steps = res.Steps
	meta.StopReason = res.StopReason
	meta.LLMCalls = res.LLMCalls
	meta.Transcript = res.Transcript
	return meta
}

// Writer appends fsync'd JSON lines to a file, safe for concurrent RunAgent workers.
type Writer struct {
	mu sync.Mutex
	f  *os.File
}

// Open opens (creating parent dirs, appending if present) the trace file.
func Open(path string) (*Writer, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f}, nil
}

// Append writes one record as a JSON line and fsyncs it.
func (w *Writer) Append(r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.f.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.f.Sync()
}

// Close closes the underlying file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
