package agent

import (
	"strings"
	"testing"
)

// ── parser branches not hit by parse_test.go ─────────────────────────────────

func TestParseActionNoInputMarkerFallback(t *testing.T) {
	// no INPUT: marker → everything after the ACTION line is the input
	a := ParseAction("ACTION: shell\nls -la /task")
	if a.Kind != "tool" || a.Tool != "shell" || a.Input != "ls -la /task" {
		t.Fatalf("got %+v", a)
	}
}

func TestParseActionStopsAtFinalDirective(t *testing.T) {
	// action's input must stop at a following FINAL, not swallow it
	a := ParseAction("ACTION: shell\nINPUT:\necho hi\nFINAL: done")
	if a.Kind != "tool" || a.Input != "echo hi" {
		t.Fatalf("input leaked into FINAL: %+v", a)
	}
}

func TestNativeToolInputScalarsAndEmpty(t *testing.T) {
	for in, want := range map[string]string{
		`42`:      "42",   // top-level number → coerce default branch
		`true`:    "true", // top-level bool
		`{}`:      "",     // empty object → ""
		`["a",1]`: "a 1",  // array with a non-string element
	} {
		if got := NativeToolInput(in); got != want {
			t.Errorf("NativeToolInput(%q)=%q want %q", in, got, want)
		}
	}
}

// ── loop branches: obs truncation + panic recovery ───────────────────────────

func TestDispatchTruncatesObservation(t *testing.T) {
	big := Tool{Name: "shell", Run: func(string) string { return strings.Repeat("x", 100) }}
	obs := dispatch(map[string]Tool{"shell": big}, "shell", "", 10)
	if len(obs) != 10 {
		t.Fatalf("expected truncation to 10, got %d", len(obs))
	}
}

func TestSafeRunRecoversPanic(t *testing.T) {
	boom := Tool{Name: "shell", Run: func(string) string { panic("kaboom") }}
	out := safeRun(boom, "x")
	if !strings.Contains(out, "ERROR running shell") || !strings.Contains(out, "kaboom") {
		t.Fatalf("panic not recovered as observation: %q", out)
	}
}

// ── tools.Schemas ────────────────────────────────────────────────────────────

func TestSchemas(t *testing.T) {
	schemas := Schemas(DefaultRegistry(&fakeExec{}))
	if len(schemas) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(schemas))
	}
	fn := schemas[0].(map[string]any)["function"].(map[string]any)
	if _, ok := fn["name"]; !ok {
		t.Fatal("schema missing function.name")
	}
	params := fn["parameters"].(map[string]any)["properties"].(map[string]any)
	if _, ok := params["input"]; !ok {
		t.Fatal("schema missing input property")
	}
}
