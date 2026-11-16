package agent

import (
	"strings"
	"testing"

	"github.com/jobordu/femto/internal/llm"
)

// pollutedReply is shaped after real TEXT-mode traces: the model writes
// its directive, then invents the tool's OUTPUT (and even a follow-up action and a
// FINAL built on that invented output) in the same reply. Before the fix, the INPUT
// ran to the next ACTION line, so bash executed "OUTPUT:", "total 12", "drwxr-xr-x …"
// ("OUTPUT:: command not found") and the step was wasted.
const pollutedReply = `I'll start by listing the challenge directory.

ACTION: shell
INPUT:
ls -la /task

OUTPUT:
total 12
drwxr-xr-x 2 root root 4096 Nov 16 12:00 .
-rw-r--r-- 1 root root   38 Nov 16 12:00 flag.txt

The flag is in flag.txt, let me read it.

ACTION: shell
INPUT:
cat /task/flag.txt
OUTPUT:
flag{this_flag_was_never_read}

FINAL: flag{this_flag_was_never_read}`

func TestParseActionCutsHallucinatedOutput(t *testing.T) {
	a := ParseAction(pollutedReply)
	if a.Kind != "tool" || a.Tool != "shell" || a.Input != "ls -la /task" {
		t.Fatalf("only the real command may run, got %+v", a)
	}
}

func TestParseActionCutsObservationVariants(t *testing.T) {
	for _, tc := range []struct{ name, msg, want string }{
		{"observation, no later directive", "ACTION: shell\nINPUT:\nid\nObservation: uid=0(root)\nso I am root", "id"},
		{"lowercase output", "ACTION: python\nINPUT:\nprint(1+1)\noutput:\n2", "print(1+1)"},
		{"markdown bold", "ACTION: shell\nINPUT:\n```bash\nfile /task/a.bin\n```\n**OUTPUT:**\n/task/a.bin: ELF 64-bit", "file /task/a.bin"},
		{"inline input", "ACTION: shell INPUT: whoami\nOBSERVATION:\nroot", "whoami"},
		{"inline command", "ACTION: shell strings /task/bin\nOutput: flag{x}", "strings /task/bin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if a := ParseAction(tc.msg); a.Kind != "tool" || a.Input != tc.want {
				t.Fatalf("want input %q, got %+v", tc.want, a)
			}
		})
	}
}

func TestParseActionKeepsOutputWordInsideCommand(t *testing.T) {
	// "output" not at the start of a line (or without a colon) is part of the command.
	msg := "ACTION: python\nINPUT:\noutput = open('/task/x').read()\nprint('OUTPUT:', output)"
	if a := ParseAction(msg); a.Input != "output = open('/task/x').read()\nprint('OUTPUT:', output)" {
		t.Fatalf("command mangled: %+v", a)
	}
}

func TestRunAgentRunsOnlyRealCommandAndDropsFakeOutputFromHistory(t *testing.T) {
	ex := &fakeExec{}
	var seen [][]llm.Message
	replies := []llm.Reply{{Content: pollutedReply}, {Content: "FINAL: flag{real}"}}
	fn := func(m []llm.Message) (llm.Reply, error) {
		seen = append(seen, append([]llm.Message(nil), m...))
		r := replies[len(seen)-1]
		return r, nil
	}
	res := RunAgent("solve it", DefaultRegistry(ex), fn, Options{})
	if len(ex.shell) != 1 || ex.shell[0] != "ls -la /task" {
		t.Fatalf("bash must run exactly the real command, ran %q", ex.shell)
	}
	if !res.Solved || res.Final != "flag{real}" {
		t.Fatalf("the FINAL built on invented output must not win: %+v", res)
	}
	hist := seen[1][2] // system, user, assistant(step 0), …
	if hist.Role != "assistant" || strings.Contains(hist.Content, "OUTPUT") ||
		strings.Contains(hist.Content, "never_read") {
		t.Fatalf("history must not keep the invented OUTPUT: %q", hist.Content)
	}
	rec := res.Transcript[0]
	if rec["assistant"] != pollutedReply || rec["cut_hallucinated_output"] != true {
		t.Fatalf("transcript must keep the raw reply and flag the cut: %+v", rec)
	}
}

func TestTextStopSequencesFitOpenAILimit(t *testing.T) {
	if len(TextStopSequences) == 0 || len(TextStopSequences) > 4 {
		t.Fatalf("OpenAI accepts at most 4 stop sequences, have %d", len(TextStopSequences))
	}
	for _, s := range TextStopSequences {
		if _, cut := CutHallucinatedOutput("ACTION: shell\nINPUT:\nls" + s + " x"); !cut {
			t.Errorf("stop sequence %q not also cut by the parser", s)
		}
	}
}
