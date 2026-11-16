package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jobordu/femto/internal/agent"
)

// env builds a getenv from a map.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// finalServer returns a server that always answers with a FINAL text reply.
func finalServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"FINAL: flag{ok}"}}]}`))
	}))
}

func TestRunMissingPrompt(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-model", "m"}, env(nil), &out, &errb); code != 2 {
		t.Fatalf("want 2, got %d", code)
	}
	if !strings.Contains(errb.String(), "-prompt is required") {
		t.Fatalf("missing usage msg: %q", errb.String())
	}
}

func TestRunMissingKey(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"-prompt", "hi"}, env(nil), &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "FEMTO_API_KEY") {
		t.Fatalf("want key error, got %d %q", code, errb.String())
	}
}

func TestRunFlagParseError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-nope"}, env(nil), &out, &errb); code != 2 {
		t.Fatalf("want 2 for bad flag, got %d", code)
	}
}

func TestRunTextSolved(t *testing.T) {
	srv := finalServer(t)
	defer srv.Close()
	var out, errb bytes.Buffer
	code := run([]string{"-prompt", "solve", "-base-url", srv.URL + "/v1", "-executor", "local"},
		env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "flag{ok}") {
		t.Fatalf("want solved, got %d out=%q err=%q", code, out.String(), errb.String())
	}
}

func TestRunJSONOutput(t *testing.T) {
	srv := finalServer(t)
	defer srv.Close()
	var out, errb bytes.Buffer
	run([]string{"-prompt", "x", "-base-url", srv.URL + "/v1", "-json"},
		env(map[string]string{"OPENAI_API_KEY": "k"}), &out, &errb)
	if !strings.Contains(out.String(), `"Solved": true`) {
		t.Fatalf("json output missing: %q", out.String())
	}
}

func TestRunNotSolvedExit1(t *testing.T) {
	// server never emits FINAL → max-steps → exit 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"still thinking"}}]}`))
	}))
	defer srv.Close()
	var out, errb bytes.Buffer
	code := run([]string{"-prompt", "x", "-base-url", srv.URL + "/v1", "-max-steps", "2"},
		env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
	if code != 1 {
		t.Fatalf("want 1 for unsolved, got %d", code)
	}
}

func TestRunLLMNewError(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"-prompt", "x", "-proxy", "://bad"},
		env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
	if code != 1 {
		t.Fatalf("want 1 for bad proxy, got %d", code)
	}
}

// fakeExec satisfies agent.Executor for the docker-seam tests.
type fakeExec struct{}

func (fakeExec) RunShell(string) string  { return "" }
func (fakeExec) RunPython(string) string { return "" }

func TestRunDockerExecutorSeam(t *testing.T) {
	srv := finalServer(t)
	defer srv.Close()
	orig := newDockerExecutor
	defer func() { newDockerExecutor = orig }()

	var gotImage string
	cleaned := false
	newDockerExecutor = func(image, mount, mem, network string) (agent.Executor, func(), error) {
		gotImage = image
		return fakeExec{}, func() { cleaned = true }, nil
	}
	var out, errb bytes.Buffer
	code := run([]string{"-prompt", "x", "-base-url", srv.URL + "/v1",
		"-executor", "docker", "-category", "crypto"},
		env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
	if code != 0 || gotImage != "femto-sbx-crypto:latest" || !cleaned {
		t.Fatalf("docker seam: code=%d image=%q cleaned=%v", code, gotImage, cleaned)
	}
}

func TestRunDockerNetworkFlag(t *testing.T) {
	srv := finalServer(t)
	defer srv.Close()
	orig := newDockerExecutor
	defer func() { newDockerExecutor = orig }()
	var gotNet string
	newDockerExecutor = func(image, mount, mem, network string) (agent.Executor, func(), error) {
		gotNet = network
		return fakeExec{}, func() {}, nil
	}
	var out, errb bytes.Buffer
	base := []string{"-prompt", "x", "-base-url", srv.URL + "/v1", "-executor", "docker"}
	e := env(map[string]string{"FEMTO_API_KEY": "k"})
	run(base, e, &out, &errb) // default: offline
	if gotNet != "none" {
		t.Fatalf("default network: want none, got %q", gotNet)
	}
	run(append(base, "-network", "cy-svc-net"), e, &out, &errb) // service challenge
	if gotNet != "cy-svc-net" {
		t.Fatalf("network flag not propagated: want cy-svc-net, got %q", gotNet)
	}
}

func TestRunDockerExecutorError(t *testing.T) {
	orig := newDockerExecutor
	defer func() { newDockerExecutor = orig }()
	newDockerExecutor = func(image, mount, mem, network string) (agent.Executor, func(), error) {
		return nil, nil, errors.New("no daemon")
	}
	var out, errb bytes.Buffer
	code := run([]string{"-prompt", "x", "-executor", "docker"},
		env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "no daemon") {
		t.Fatalf("want docker error exit 1, got %d %q", code, errb.String())
	}
}

func TestRunNativeMode(t *testing.T) {
	// native mode + a reply with no tool_calls → content is the final answer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"flag{native}"}}]}`))
	}))
	defer srv.Close()
	var out, errb bytes.Buffer
	code := run([]string{"-prompt", "x", "-base-url", srv.URL + "/v1", "-native"},
		env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "flag{native}") {
		t.Fatalf("native run failed: %d %q", code, out.String())
	}
}

func TestRunDockerImageOverride(t *testing.T) {
	srv := finalServer(t)
	defer srv.Close()
	orig := newDockerExecutor
	defer func() { newDockerExecutor = orig }()
	var gotImage string
	newDockerExecutor = func(image, mount, mem, network string) (agent.Executor, func(), error) {
		gotImage = image
		return fakeExec{}, func() {}, nil
	}
	var out, errb bytes.Buffer
	run([]string{"-prompt", "x", "-base-url", srv.URL + "/v1", "-executor", "docker",
		"-image", "my-custom-sbx:1"}, env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
	if gotImage != "my-custom-sbx:1" { // explicit -image wins over category routing
		t.Fatalf("image override ignored: %q", gotImage)
	}
}

// TestMainWrapper re-execs the test binary so the 3-line main() os.Exit wrapper is
// actually executed (no -prompt → run returns 2 → os.Exit(2)).
func TestMainWrapper(t *testing.T) {
	if os.Getenv("FEMTO_TEST_MAIN") == "1" {
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainWrapper")
	cmd.Env = append(os.Environ(), "FEMTO_TEST_MAIN=1")
	if dir := os.Getenv("GOCOVERDIR"); dir != "" { // let child coverage merge if enabled
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
	}
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Fatalf("expected main() to exit 2, got %v", err)
	}
}

func TestRunEmitsTrace(t *testing.T) {
	srv := finalServer(t)
	defer srv.Close()
	orig := nowUnix
	nowUnix = func() float64 { return 42.0 }
	defer func() { nowUnix = orig }()

	tracePath := t.TempDir() + "/traces.jsonl"
	var out, errb bytes.Buffer
	code := run([]string{"-prompt", "solve", "-base-url", srv.URL + "/v1",
		"-trace", tracePath, "-task-id", "chal-1", "-benchmark", "cybench", "-model", "m1"},
		env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
	if code != 0 {
		t.Fatalf("run failed: %d %q", code, errb.String())
	}
	data, _ := os.ReadFile(tracePath)
	var rec map[string]any
	if err := json.Unmarshal(bytesFirstLine(data), &rec); err != nil {
		t.Fatalf("trace not valid JSON: %v", err)
	}
	if rec["ts"] != 42.0 || rec["task_id"] != "chal-1" || rec["benchmark"] != "cybench" ||
		rec["model"] != "m1" || rec["solved"] != true || rec["final"] != "flag{ok}" {
		t.Fatalf("trace record wrong: %+v", rec)
	}
}

func TestRunTraceOpenError(t *testing.T) {
	srv := finalServer(t)
	defer srv.Close()
	// trace path under an existing file → Open fails → run returns 1
	f := t.TempDir() + "/afile"
	os.WriteFile(f, []byte("x"), 0o644)
	var out, errb bytes.Buffer
	code := run([]string{"-prompt", "x", "-base-url", srv.URL + "/v1", "-trace", f + "/t.jsonl"},
		env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "trace:") {
		t.Fatalf("expected trace error exit 1, got %d %q", code, errb.String())
	}
}

func bytesFirstLine(b []byte) []byte {
	for i, c := range b {
		if c == '\n' {
			return b[:i]
		}
	}
	return b
}

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-version"}, env(nil), &out, &errb); code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	if !strings.Contains(out.String(), "femto") {
		t.Fatalf("version output missing: %q", out.String())
	}
}

func TestFirstEnv(t *testing.T) {
	g := env(map[string]string{"B": "second"})
	if got := firstEnv(g, "A", "B", "C"); got != "second" {
		t.Fatalf("firstEnv fallthrough failed: %q", got)
	}
	if got := firstEnv(g, "X"); got != "" {
		t.Fatalf("firstEnv empty failed: %q", got)
	}
}

func TestRunParseMode(t *testing.T) {
	// -parse re-executes the ReAct parser deterministically (no LLM/key) — the
	// verification primitive the neutrality judge uses to confirm/refute HARNESS claims.
	var out, errb bytes.Buffer
	code := run([]string{"-parse", "-prompt", "Let's try. ACTION: shell INPUT: ls"},
		env(nil), &out, &errb)
	if code != 0 || !strings.Contains(out.String(), `"Kind":"tool"`) ||
		!strings.Contains(out.String(), `"Tool":"shell"`) {
		t.Fatalf("parse tool: code=%d out=%q", code, out.String())
	}
	out.Reset()
	// prose with no directive -> "none": a HARNESS claim on this text would be refuted.
	run([]string{"-parse", "-prompt", "I think the flag is here."}, env(nil), &out, &errb)
	if !strings.Contains(out.String(), `"Kind":"none"`) {
		t.Fatalf("parse none: %q", out.String())
	}
}

// TestRunStopSequencesTextModeOnly: text mode asks the provider to halt before a
// model-written OUTPUT block; native mode has no such block and sends none.
func TestRunStopSequencesTextModeOnly(t *testing.T) {
	for _, native := range []bool{false, true} {
		var raw map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&raw)
			w.Write([]byte(`{"choices":[{"message":{"content":"FINAL: flag{ok}"}}]}`))
		}))
		args := []string{"-prompt", "solve", "-base-url", srv.URL + "/v1", "-executor", "local"}
		if native {
			args = append(args, "-native")
		}
		var out, errb bytes.Buffer
		run(args, env(map[string]string{"FEMTO_API_KEY": "k"}), &out, &errb)
		srv.Close()
		stop, has := raw["stop"].([]any)
		if native && has {
			t.Errorf("native mode must not send stop, got %v", raw["stop"])
		}
		if !native && (len(stop) != len(agent.TextStopSequences) || stop[0] != agent.TextStopSequences[0]) {
			t.Errorf("text mode must send TextStopSequences, got %v", raw["stop"])
		}
	}
}
