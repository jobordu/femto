package agent

import "testing"

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
