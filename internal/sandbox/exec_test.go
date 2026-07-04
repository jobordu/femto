package sandbox

import (
	"context"
	"strings"
	"testing"
)

func TestImageForCategory(t *testing.T) {
	cases := map[string]string{
		"web":     "femto-sbx-lite:latest",
		"MISC":    "femto-sbx-lite:latest", // case-insensitive
		"crypto":  "femto-sbx-crypto:latest",
		" pwn ":   "femto-sbx-pwn:latest", // trimmed
		"reverse": "femto-sbx-pwn:latest",
		"":        FullImage, // untagged → safe full default
		"kitchen": FullImage, // unknown → safe full default
	}
	for cat, want := range cases {
		if got := ImageForCategory(cat, nil, ""); got != want {
			t.Errorf("ImageForCategory(%q)=%q want %q", cat, got, want)
		}
	}
}

func TestImageForCategoryCustomMapAndFull(t *testing.T) {
	imgs := map[string]string{"web": "custom-web:1"}
	if got := ImageForCategory("web", imgs, "myfull"); got != "custom-web:1" {
		t.Errorf("custom map ignored: %q", got)
	}
	if got := ImageForCategory("nope", imgs, "myfull"); got != "myfull" {
		t.Errorf("custom full ignored: %q", got)
	}
}

func TestLocalExecutorRunsWithInjectedRunner(t *testing.T) {
	var gotArgv []string
	var gotStdin string
	ex := &LocalExecutor{Timeout: 5e9, run: func(_ context.Context, argv []string, stdin string) string {
		gotArgv, gotStdin = argv, stdin
		return "OUT"
	}}
	if out := ex.RunShell("ls -la"); out != "OUT" {
		t.Fatalf("shell out %q", out)
	}
	if gotArgv[0] != "bash" || gotArgv[len(gotArgv)-1] != "ls -la" {
		t.Fatalf("shell argv %+v", gotArgv)
	}
	ex.RunPython("print(1)")
	if gotArgv[0] != "python3" || gotStdin != "print(1)" {
		t.Fatalf("python argv %+v stdin %q", gotArgv, gotStdin)
	}
}

func TestLocalExecutorRealEcho(t *testing.T) {
	// smoke the real runner (no injection) — bash must be present
	ex := NewLocalExecutor("", 5e9)
	if out := ex.RunShell("echo femto"); strings.TrimSpace(out) != "femto" {
		t.Fatalf("real echo got %q", out)
	}
}

func TestDockerSandboxStartArgvAndLifecycle(t *testing.T) {
	var calls [][]string
	box := NewDockerSandbox("femto-sbx-lite:latest", "/host/task")
	box.Memory = "256m"
	box.run = func(_ context.Context, argv []string, _ string) string {
		calls = append(calls, argv)
		if argv[0] == "docker" && argv[1] == "run" {
			return "container_deadbeef\n"
		}
		return "exec-out"
	}
	if err := box.Start(); err != nil {
		t.Fatal(err)
	}
	run := calls[0]
	assertHasPair(t, run, "--network", "none")
	assertHasPair(t, run, "--memory", "256m")
	assertHasPair(t, run, "--memory-swap", "256m") // hard cap: swap disabled
	assertHasPair(t, run, "-v", "/host/task:/task:rw")
	if run[len(run)-2] != "sleep" {
		t.Fatalf("expected sleep ttl, got %+v", run[len(run)-3:])
	}
	if box.cid != "container_deadbeef" {
		t.Fatalf("cid not captured: %q", box.cid)
	}
	if out := box.RunShell("id"); out != "exec-out" {
		t.Fatalf("exec out %q", out)
	}
	if ex := calls[1]; ex[0] != "docker" || ex[1] != "exec" || ex[3] != "container_deadbeef" {
		t.Fatalf("exec argv %+v", ex)
	}
	box.Stop()
	if box.cid != "" {
		t.Fatal("cid not cleared on stop")
	}
	last := calls[len(calls)-1]
	if last[1] != "rm" || last[2] != "-f" {
		t.Fatalf("expected docker rm -f, got %+v", last)
	}
}

func TestDockerSandboxStartFailure(t *testing.T) {
	box := NewDockerSandbox("img", "")
	box.run = func(_ context.Context, _ []string, _ string) string { return "ERROR: no daemon" }
	if err := box.Start(); err == nil {
		t.Fatal("expected start error")
	}
}

func TestDockerSandboxExecBeforeStart(t *testing.T) {
	box := NewDockerSandbox("img", "")
	if out := box.RunShell("x"); out != "ERROR: sandbox not started" {
		t.Fatalf("got %q", out)
	}
}

func assertHasPair(t *testing.T, argv []string, flag, val string) {
	t.Helper()
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == val {
			return
		}
	}
	t.Fatalf("missing %s %s in %+v", flag, val, argv)
}
