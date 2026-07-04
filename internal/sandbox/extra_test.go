package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRealRunErrorNoOutput(t *testing.T) {
	// command fails with no output → "ERROR: ..." branch
	out := realRun(context.Background(), []string{"false"}, "")
	if !strings.HasPrefix(out, "ERROR:") {
		t.Fatalf("expected ERROR, got %q", out)
	}
}

func TestRealRunTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	out := realRun(ctx, []string{"sleep", "5"}, "")
	if out != "ERROR: timed out" {
		t.Fatalf("expected timeout, got %q", out)
	}
}

func TestRealRunWithStdin(t *testing.T) {
	// `cat` echoes stdin → exercises the stdin != "" branch of realRun
	out := realRun(context.Background(), []string{"cat"}, "piped-in")
	if strings.TrimSpace(out) != "piped-in" {
		t.Fatalf("stdin not piped: %q", out)
	}
}

func TestNewLocalExecutorDefaultTimeout(t *testing.T) {
	if e := NewLocalExecutor("", 0); e.Timeout != 15*time.Second {
		t.Fatalf("default timeout not applied: %v", e.Timeout)
	}
}

func TestLocalExecutorWorkdirPrefix(t *testing.T) {
	var gotArgv []string
	e := &LocalExecutor{Workdir: "/work", Timeout: time.Second,
		run: func(_ context.Context, argv []string, _ string) string { gotArgv = argv; return "" }}
	e.RunShell("ls")
	if gotArgv[0] != "env" || gotArgv[1] != "-C" || gotArgv[2] != "/work" {
		t.Fatalf("workdir prefix missing: %+v", gotArgv)
	}
}

func TestDockerMemorySwapOverride(t *testing.T) {
	box := NewDockerSandbox("img", "")
	box.Memory = "256m"
	box.MemorySwap = "512m" // explicit override wins over the ==Memory default
	if box.memorySwap() != "512m" {
		t.Fatalf("memorySwap override ignored: %q", box.memorySwap())
	}
	argv := box.startArgv("n")
	assertHasPair(t, argv, "--memory-swap", "512m")
}

func TestDockerRunPython(t *testing.T) {
	var gotArgv []string
	var gotStdin string
	box := NewDockerSandbox("img", "")
	box.run = func(_ context.Context, argv []string, stdin string) string {
		gotArgv, gotStdin = argv, stdin
		if argv[1] == "run" {
			return "cid123\n"
		}
		return "py-out"
	}
	_ = box.Start()
	if out := box.RunPython("print(1)"); out != "py-out" {
		t.Fatalf("python out %q", out)
	}
	if gotArgv[0] != "docker" || gotArgv[1] != "exec" || gotStdin != "print(1)" {
		t.Fatalf("python exec argv %+v stdin %q", gotArgv, gotStdin)
	}
}

func TestDockerStopBeforeStartIsNoop(t *testing.T) {
	called := false
	box := NewDockerSandbox("img", "")
	box.run = func(_ context.Context, _ []string, _ string) string { called = true; return "" }
	box.Stop() // cid == "" → no command
	if called {
		t.Fatal("Stop ran a command before Start")
	}
}

func TestStartErrorMessage(t *testing.T) {
	box := NewDockerSandbox("img", "")
	box.run = func(_ context.Context, _ []string, _ string) string { return "" } // empty → start fails
	err := box.Start()
	if err == nil || !strings.Contains(err.Error(), "sandbox start failed") {
		t.Fatalf("expected start-failed error, got %v", err)
	}
}
