package agent

import (
	"errors"
	"testing"

	"github.com/jobordu/femto/internal/llm"
)

// fakeExec records calls and returns canned output.
type fakeExec struct{ shell, py []string }

func (f *fakeExec) RunShell(cmd string) string {
	f.shell = append(f.shell, cmd)
	return "shell-out:" + cmd
}
func (f *fakeExec) RunPython(code string) string { f.py = append(f.py, code); return "py-out" }

// scripted returns replies in order, then errors if over-called.
func scripted(replies ...llm.Reply) LLMFn {
	i := 0
	return func([]llm.Message) (llm.Reply, error) {
		if i >= len(replies) {
			return llm.Reply{}, errors.New("over-called")
		}
		r := replies[i]
		i++
		return r, nil
	}
}

func TestRunAgentTextToolThenFinal(t *testing.T) {
	ex := &fakeExec{}
	tools := DefaultRegistry(ex)
	fn := scripted(
		llm.Reply{Content: "ACTION: shell\nINPUT:\ncat flag"},
		llm.Reply{Content: "FINAL: flag{win}"},
	)
	res := RunAgent("solve it", tools, fn, Options{})
	if !res.Solved || res.Final != "flag{win}" || res.Steps != 2 {
		t.Fatalf("got %+v", res)
	}
	if len(ex.shell) != 1 || ex.shell[0] != "cat flag" {
		t.Fatalf("tool not dispatched: %+v", ex.shell)
	}
}

func TestRunAgentUnknownToolIsObservable(t *testing.T) {
	fn := scripted(
		llm.Reply{Content: "ACTION: nmap\nINPUT:\n-p 80"},
		llm.Reply{Content: "FINAL: done"},
	)
	res := RunAgent("x", DefaultRegistry(&fakeExec{}), fn, Options{})
	if !res.Solved { // unknown tool → error observation, loop continues, then FINAL
		t.Fatalf("expected solved after recovering, got %+v", res)
	}
}

func TestRunAgentNudgeOnNoAction(t *testing.T) {
	fn := scripted(
		llm.Reply{Content: "just chatting, no directive"},
		llm.Reply{Content: "FINAL: ok"},
	)
	res := RunAgent("x", DefaultRegistry(&fakeExec{}), fn, Options{})
	if !res.Solved || res.Transcript[0]["nudge"] != true {
		t.Fatalf("expected nudge then final: %+v", res)
	}
}

func TestRunAgentMaxSteps(t *testing.T) {
	// always emits a tool call, never finals
	fn := func([]llm.Message) (llm.Reply, error) {
		return llm.Reply{Content: "ACTION: shell\nINPUT:\nls"}, nil
	}
	res := RunAgent("x", DefaultRegistry(&fakeExec{}), fn, Options{MaxSteps: 3})
	if res.Solved || res.StopReason != "max_steps" || res.Steps != 3 {
		t.Fatalf("got %+v", res)
	}
}

func TestRunAgentNativeMode(t *testing.T) {
	ex := &fakeExec{}
	tc := llm.ToolCall{ID: "c1"}
	tc.Function.Name = "functions.shell"
	tc.Function.Arguments = `{"input":"id"}`
	fn := scripted(
		llm.Reply{ToolCalls: []llm.ToolCall{tc}},
		llm.Reply{Content: "flag{native}"}, // no tool call → final
	)
	res := RunAgent("x", DefaultRegistry(ex), fn, Options{Native: true})
	if !res.Solved || res.Final != "flag{native}" {
		t.Fatalf("got %+v", res)
	}
	if len(ex.shell) != 1 || ex.shell[0] != "id" {
		t.Fatalf("native dispatch failed: %+v", ex.shell)
	}
}

func TestRunAgentLLMErrorSurfaces(t *testing.T) {
	fn := func([]llm.Message) (llm.Reply, error) { return llm.Reply{}, errors.New("boom") }
	res := RunAgent("x", DefaultRegistry(&fakeExec{}), fn, Options{LLMRetries: 1})
	if res.Solved || res.StopReason != "error:llm" {
		t.Fatalf("got %+v", res)
	}
	if res.LLMCalls != 2 { // 1 try + 1 retry
		t.Fatalf("expected 2 llm calls, got %d", res.LLMCalls)
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	tools := DefaultRegistry(&fakeExec{})
	if p := BuildSystemPrompt(tools, false); !contains(p, "ACTION:") || !contains(p, "shell") {
		t.Fatal("text prompt missing protocol/tools")
	}
	if p := BuildSystemPrompt(tools, true); contains(p, "ACTION:") {
		t.Fatal("native prompt should not describe the text protocol")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
