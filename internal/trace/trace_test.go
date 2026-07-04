package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jobordu/femto/internal/agent"
)

func TestFromResultCopiesOutcome(t *testing.T) {
	res := agent.AgentResult{Solved: true, Final: "flag{x}", Steps: 3, StopReason: "final",
		LLMCalls: 4, Transcript: []map[string]any{{"step": 0}}}
	r := FromResult(res, Record{Model: "m", TaskID: "t1"}, 123.5)
	if r.TS != 123.5 || !r.Solved || r.Final != "flag{x}" || r.Steps != 3 ||
		r.StopReason != "final" || r.LLMCalls != 4 || r.Model != "m" || r.TaskID != "t1" {
		t.Fatalf("record not populated: %+v", r)
	}
	if len(r.Transcript) != 1 {
		t.Fatalf("transcript dropped: %+v", r.Transcript)
	}
}

func TestWriterAppendsFsyncdLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "traces.jsonl")
	w, err := Open(path) // creates nested dir
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := w.Append(Record{Model: "m", Steps: i}); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	// reopen appends, doesn't truncate
	w2, _ := Open(path)
	w2.Append(Record{Model: "m2"})
	w2.Close()

	data, _ := os.ReadFile(path)
	var n int
	for _, ln := range splitLines(data) {
		if len(ln) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(ln, &r); err != nil {
			t.Fatalf("bad line %q: %v", ln, err)
		}
		n++
	}
	if n != 4 {
		t.Fatalf("expected 4 appended records, got %d", n)
	}
}

func TestOpenBadPath(t *testing.T) {
	// a path whose parent is an existing file → MkdirAll fails
	f := filepath.Join(t.TempDir(), "afile")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, err := Open(filepath.Join(f, "traces.jsonl")); err == nil {
		t.Fatal("expected open error under a file-as-dir")
	}
}

func TestOpenPathIsDirectory(t *testing.T) {
	// path is an existing directory → MkdirAll ok, but OpenFile(O_WRONLY) fails
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("expected open error on a directory path")
	}
}

func TestAppendMarshalError(t *testing.T) {
	w, _ := Open(filepath.Join(t.TempDir(), "t.jsonl"))
	defer w.Close()
	// a channel in the transcript is not JSON-marshalable → Marshal error branch
	rec := Record{Model: "m", Transcript: []map[string]any{{"bad": make(chan int)}}}
	if err := w.Append(rec); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestAppendAfterCloseErrors(t *testing.T) {
	w, _ := Open(filepath.Join(t.TempDir(), "t.jsonl"))
	w.Close()
	if err := w.Append(Record{Model: "m"}); err == nil {
		t.Fatal("expected write error on closed file")
	}
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
