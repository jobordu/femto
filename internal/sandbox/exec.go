// Package sandbox provides executors for the agent's tool calls: LocalExecutor (bare
// subprocess, trusted use only) and DockerSandbox (a disposable network-isolated
// per-task container). It also routes a challenge category to a specialized, smaller
// sandbox image — most CTF tasks don't need the full toolchain.
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// randID returns a short random hex id for container names (stdlib-only, no uuid dep).
func randID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ── category → specialized image routing ─────────────────────────────────────
//
// The win is varying the BASE, not just trimming tools: web/misc/shell need no
// Python (tiny alpine image), crypto needs python+crypto libs, pwn/rev/forensics
// need the heavy binary toolchain. Unknown/blank categories fall back to the FULL
// image (network-off means a missing tool can't be installed — safety first).

// DefaultImages maps a challenge category to its specialized sandbox image.
var DefaultImages = map[string]string{
	"web":       "femto-sbx-lite:latest",
	"misc":      "femto-sbx-lite:latest",
	"shell":     "femto-sbx-lite:latest",
	"crypto":    "femto-sbx-crypto:latest",
	"ppc":       "femto-sbx-crypto:latest",
	"pwn":       "femto-sbx-pwn:latest",
	"reverse":   "femto-sbx-pwn:latest",
	"rev":       "femto-sbx-pwn:latest",
	"forensics": "femto-sbx-pwn:latest",
}

// FullImage is the safe default: everything baked in, for unknown/multi-category tasks.
const FullImage = "femto-sbx-full:latest"

// ImageForCategory picks the sandbox image for a category. A blank/unknown category
// (e.g. the untagged picoCTF tasks) routes to the full image so a mis-tagged
// challenge never lands in a sandbox missing the tool it needs.
func ImageForCategory(category string, images map[string]string, full string) string {
	if images == nil {
		images = DefaultImages
	}
	if full == "" {
		full = FullImage
	}
	if img, ok := images[strings.ToLower(strings.TrimSpace(category))]; ok {
		return img
	}
	return full
}

// runCmd runs argv with a timeout, merging stdout+stderr — the shape both executors
// return. Injectable so callers/tests can stub it.
type runFn func(ctx context.Context, argv []string, stdin string) string

func realRun(ctx context.Context, argv []string, stdin string) string {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "ERROR: timed out"
	}
	if err != nil && len(out) == 0 {
		return "ERROR: " + err.Error()
	}
	return string(out)
}

// ── LocalExecutor: bare subprocess, NO isolation. Trusted tasks + tests only. ──
type LocalExecutor struct {
	Workdir string
	Timeout time.Duration
	run     runFn
}

func NewLocalExecutor(workdir string, timeout time.Duration) *LocalExecutor {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &LocalExecutor{Workdir: workdir, Timeout: timeout, run: realRun}
}

func (e *LocalExecutor) exec(argv []string, stdin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), e.Timeout)
	defer cancel()
	if e.Workdir != "" {
		argv = append([]string{"env", "-C", e.Workdir}, argv...)
	}
	return e.run(ctx, argv, stdin)
}

func (e *LocalExecutor) RunShell(cmd string) string {
	return e.exec([]string{"bash", "-lc", cmd}, "")
}

func (e *LocalExecutor) RunPython(code string) string {
	return e.exec([]string{"python3", "-"}, code)
}

// ── DockerSandbox: disposable, network-isolated, resource-capped container. ────
type DockerSandbox struct {
	Image       string
	Mount       string
	Network     string // "none" by default
	Memory      string // hard-capped: MemorySwap defaults to == Memory
	MemorySwap  string
	CPUs        string
	Pids        int
	TTL         int           // container lifetime (seconds) before self-exit
	ExecTimeout time.Duration // per tool call
	run         runFn
	cid         string
}

// NewDockerSandbox builds a sandbox for one task. Defaults mirror the flynn agent:
// network off, 256m RAM (swap disabled), 1 CPU, pids 256.
func NewDockerSandbox(image, mount string) *DockerSandbox {
	return &DockerSandbox{
		Image: image, Mount: mount, Network: "none",
		Memory: "256m", CPUs: "1.0", Pids: 256, TTL: 1800,
		ExecTimeout: 30 * time.Second, run: realRun,
	}
}

func (s *DockerSandbox) memorySwap() string {
	if s.MemorySwap != "" {
		return s.MemorySwap
	}
	return s.Memory // == memory ⇒ swap disabled ⇒ a true RAM ceiling
}

// startArgv is the `docker run` command (exposed for testing without a daemon).
func (s *DockerSandbox) startArgv(name string) []string {
	argv := []string{"docker", "run", "-d", "--rm", "--name", name,
		"--network", s.Network, "--memory", s.Memory, "--memory-swap", s.memorySwap(),
		"--cpus", s.CPUs, "--pids-limit", strconv.Itoa(s.Pids), "-w", "/task"}
	if s.Mount != "" {
		argv = append(argv, "-v", s.Mount+":/task:rw")
	}
	return append(argv, s.Image, "sleep", strconv.Itoa(s.TTL))
}

// Start launches the container and records its id.
func (s *DockerSandbox) Start() error {
	name := "femto-sbx-" + randID()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out := s.run(ctx, s.startArgv(name), "")
	out = strings.TrimSpace(out)
	if strings.HasPrefix(out, "ERROR") || out == "" {
		return &startError{out}
	}
	lines := strings.Split(out, "\n")
	s.cid = strings.TrimSpace(lines[len(lines)-1])
	return nil
}

func (s *DockerSandbox) execIn(argv []string, stdin string) string {
	if s.cid == "" {
		return "ERROR: sandbox not started"
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.ExecTimeout)
	defer cancel()
	full := append([]string{"docker", "exec", "-i", s.cid}, argv...)
	return s.run(ctx, full, stdin)
}

func (s *DockerSandbox) RunShell(cmd string) string {
	return s.execIn([]string{"bash", "-lc", cmd}, "")
}

func (s *DockerSandbox) RunPython(code string) string {
	return s.execIn([]string{"python3", "-"}, code)
}

// Stop removes the container (best effort).
func (s *DockerSandbox) Stop() {
	if s.cid == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.run(ctx, []string{"docker", "rm", "-f", s.cid}, "")
	s.cid = ""
}

type startError struct{ out string }

func (e *startError) Error() string { return "sandbox start failed: " + e.out }
