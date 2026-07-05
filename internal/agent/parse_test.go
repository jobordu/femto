package agent

import (
	"strings"
	"testing"
)

func TestParseActionFinalWins(t *testing.T) {
	a := ParseAction("thinking...\nFINAL: flag{abc}")
	if a.Kind != "final" || a.Final != "flag{abc}" {
		t.Fatalf("got %+v", a)
	}
}

func TestParseActionToolBlock(t *testing.T) {
	a := ParseAction("ACTION: shell\nINPUT:\nls -la /task")
	if a.Kind != "tool" || a.Tool != "shell" || a.Input != "ls -la /task" {
		t.Fatalf("got %+v", a)
	}
}

func TestParseActionInlineInput(t *testing.T) {
	// weak models put it all on one line — the same-line input must be recovered
	a := ParseAction("ACTION: shell INPUT: cat flag.txt")
	if a.Kind != "tool" || a.Tool != "shell" || a.Input != "cat flag.txt" {
		t.Fatalf("got %+v", a)
	}
}

func TestParseActionInlineFenceBodyFollows(t *testing.T) {
	// inline INPUT opens a fence not closed on the line → body is on following lines
	a := ParseAction("ACTION: python INPUT: ```\nprint(41+1)\n```")
	if a.Kind != "tool" || a.Tool != "python" || a.Input != "print(41+1)" {
		t.Fatalf("got %+v", a)
	}
}

func TestParseActionMashedMarkersNoTool(t *testing.T) {
	// "ACTION:/INPUT:" with no tool between must NOT parse tool="INPUT"
	a := ParseAction("ACTION:\nINPUT:\nsomething")
	if a.Kind != "none" {
		t.Fatalf("expected none, got %+v", a)
	}
}

func TestParseActionMarkdownDecoration(t *testing.T) {
	a := ParseAction("**ACTION:** `shell`\n**INPUT:**\nwhoami")
	if a.Kind != "tool" || a.Tool != "shell" || a.Input != "whoami" {
		t.Fatalf("got %+v", a)
	}
}

func TestParseActionStopsAtNextDirective(t *testing.T) {
	// first action wins; its input stops at the next ACTION, not swallowing it
	a := ParseAction("ACTION: shell\nINPUT:\necho hi\nACTION: python\nINPUT:\nx=1")
	if a.Input != "echo hi" {
		t.Fatalf("input leaked past next directive: %q", a.Input)
	}
}

func TestParseActionProseDoesNotFalseMatch(t *testing.T) {
	if a := ParseAction("I will use the shell tool to look around."); a.Kind != "none" {
		t.Fatalf("prose false-matched: %+v", a)
	}
}

func TestLookupToolAliases(t *testing.T) {
	tools := map[string]Tool{"shell": {Name: "shell"}, "python": {Name: "python"}}
	for name, want := range map[string]string{
		"bash": "shell", "sh": "shell", "python3": "python", "PY": "python", "shell": "shell",
	} {
		if tool, ok := LookupTool(tools, name); !ok || tool.Name != want {
			t.Errorf("%s -> %v/%v want %s", name, tool.Name, ok, want)
		}
	}
	if _, ok := LookupTool(tools, "nmap"); ok {
		t.Error("unknown tool resolved")
	}
}

func TestCleanNativeName(t *testing.T) {
	for in, want := range map[string]string{
		"shell":                      "shell",
		"functions.shell":            "shell",
		"shell<|channel|>commentary": "shell",
		"functions.python<|call|>":   "python",
	} {
		if got := CleanNativeName(in); got != want {
			t.Errorf("CleanNativeName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNativeToolInput(t *testing.T) {
	cases := map[string]string{
		`{"input":"ls"}`:   "ls",
		`{"cmd":"whoami"}`: "whoami",
		`{"command":"id"}`: "id",
		`["cat","flag"]`:   "cat flag",
		`"bare string"`:    "bare string",
		`null`:             "",
		`{"x":{"a":1}}`:    `{"a":1}`,
		`not json at all`:  "not json at all",
	}
	for in, want := range cases {
		if got := NativeToolInput(in); got != want {
			t.Errorf("NativeToolInput(%q)=%q want %q", in, got, want)
		}
	}
}

func TestParseActionInlineAfterProse(t *testing.T) {
	// The real trace fault: capable models write the directive after a sentence.
	// Recovered only because an INPUT: marker disambiguates it from prose.
	a := ParseAction(`It seems there is a syntax error. Let's try to fix it. ACTION: python INPUT: print("hi")`)
	if a.Kind != "tool" || a.Tool != "python" || a.Input != `print("hi")` {
		t.Fatalf("inline-after-prose not recovered: %+v", a)
	}
	a = ParseAction(`hexdump not available. Let's use od instead. ACTION: shell INPUT: od -c /task/f`)
	if a.Kind != "tool" || a.Tool != "shell" || a.Input != "od -c /task/f" {
		t.Fatalf("inline shell not recovered: %+v", a)
	}
}

func TestParseActionInlineProseNoInputStaysNone(t *testing.T) {
	// A prose mention of ACTION: with no INPUT marker must NOT hijack the parse.
	for _, s := range []string{
		"I will use the ACTION: format to solve this problem next.",
		"The correct action: think carefully before answering.",
	} {
		if a := ParseAction(s); a.Kind != "none" {
			t.Errorf("prose false-positive on %q: %+v", s, a)
		}
	}
}

func TestParseActionAnchoredStillPreferred(t *testing.T) {
	// A proper line-anchored directive wins even if prose earlier mentions ACTION.
	a := ParseAction("I considered an action: earlier.\nACTION: shell\nINPUT: ls")
	if a.Kind != "tool" || a.Tool != "shell" || a.Input != "ls" {
		t.Fatalf("anchored directive not preferred: %+v", a)
	}
}

func TestParseActionFinalViaActionFrame(t *testing.T) {
	// Models that finalize through the ACTION frame (real trace: mixtral-8x7b emitted
	// "ACTION: FINAL\nINPUT: CTF{...}"). FINAL is a directive, not a tool.
	cases := map[string]string{
		"ACTION: FINAL\nINPUT:\nCTF{flag}":            "CTF{flag}",
		"ACTION: FINAL: CTF{flag}":                     "CTF{flag}",
		"ACTION: FINAL CTF{flag}":                      "CTF{flag}",
		"ACTION: FINAL\nCTF{flag}":                     "CTF{flag}",
		"prose\n\nACTION: FINAL\nINPUT:\nthe answer":   "the answer",
		"TOOL: final: done":                            "done",
	}
	for in, want := range cases {
		a := ParseAction(in)
		if a.Kind != "final" || a.Final != want {
			t.Errorf("action-final %q -> %+v, want final %q", in, a, want)
		}
	}
}

func TestParseActionFinalPrecedence(t *testing.T) {
	// A real tool that appears BEFORE an action-final wins (first directive wins).
	a := ParseAction("ACTION: shell\nINPUT: ls\nACTION: FINAL: done")
	if a.Kind != "tool" || a.Tool != "shell" {
		t.Errorf("tool-before-final: %+v", a)
	}
	// A line-anchored FINAL earlier than an action-tool still wins (final kind).
	a = ParseAction("FINAL: x\nACTION: shell\nINPUT: ls")
	if a.Kind != "final" || !strings.HasPrefix(a.Final, "x") {
		t.Errorf("anchored-final-first: %+v", a)
	}
	// action-final answer on following lines is bounded by the next directive.
	a = ParseAction("ACTION: FINAL\nthe answer\nACTION: shell\nINPUT: ls")
	if a.Kind != "final" || a.Final != "the answer" {
		t.Errorf("action-final bounded by next directive: %+v", a)
	}
}

func TestParseActionInlineWithNextLineInput(t *testing.T) {
	// Class B (real traces: minimax-m3, llama-3.2-3b): the ACTION is glued mid-prose
	// with INPUT on the FOLLOWING line. Recovered because INPUT: is on the next line.
	a := ParseAction("I'll analyze step by step.ACTION: shell\nINPUT:\nls -la /task/")
	if a.Kind != "tool" || a.Tool != "shell" || a.Input != "ls -la /task/" {
		t.Fatalf("inline+next-line-input: %+v", a)
	}
	// guard: prose that mentions ACTION: with NO nearby INPUT stays none.
	if ParseAction("I will use the ACTION: format later.").Kind != "none" {
		t.Error("prose ACTION false-positive")
	}
}

func TestParseToolCallWrapper(t *testing.T) {
	// Class A (real traces: minimax-m2.7): the model's own "<...tool_call>" wrapper.
	cases := []struct{ in, tool, input string }{
		{"x\n\n<minimax:tool_call>\nshell\nINPUT:\ncat f", "shell", "cat f"},
		{"<minimax:tool_call>: python\nINPUT:\nprint(1)", "python", "print(1)"},
		{"<tool_call>\nshell\nINPUT: id", "shell", "id"},
		{"<TOOLCALL: shell\nINPUT: ls /task", "shell", "ls /task"},
	}
	for _, c := range cases {
		a := ParseAction(c.in)
		if a.Kind != "tool" || a.Tool != c.tool || a.Input != c.input {
			t.Errorf("tool_call %q -> %+v want tool=%q input=%q", c.in, a, c.tool, c.input)
		}
	}
	// mixed "<tool_call> ACTION: shell" is handled by the ACTION path, not the wrapper.
	a := ParseAction("<minimax:tool_call> ACTION: shell\nINPUT: ls")
	if a.Kind != "tool" || a.Tool != "shell" {
		t.Errorf("mixed wrapper+action: %+v", a)
	}
	// a wrapper with no resolvable tool name -> none (no next-line tool).
	if ParseAction("<tool_call>\n\nsome prose here").Kind != "none" {
		t.Error("wrapper with no tool should be none")
	}
	// a bare wrapper with nothing after it at all -> none.
	if ParseAction("<minimax:tool_call>").Kind != "none" {
		t.Error("bare wrapper should be none")
	}
	// a wrapper whose inline text is an ACTION keyword is skipped (deferred to the ACTION
	// path); a later clean wrapper still wins.
	a = ParseAction("<tool_call> ACTION\n<tool_call>\nshell\nINPUT: id")
	if a.Kind != "tool" || a.Tool != "shell" {
		t.Errorf("skip-action-wrapper: %+v", a)
	}
}
