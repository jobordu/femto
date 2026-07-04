// femto — a tiny (~3MB static) provider-agnostic ReAct agent. Drives any
// OpenAI-compatible endpoint (OpenAI, vLLM, Ollama, NVIDIA NIM, …) through a
// think→act→observe loop over a shell+python toolset, in text or native tool-calling
// mode.
//
//	FEMTO_API_KEY=sk-... femto -prompt "capture the flag in /task"
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jobordu/femto/internal/agent"
	"github.com/jobordu/femto/internal/llm"
	"github.com/jobordu/femto/internal/sandbox"
	"github.com/jobordu/femto/internal/trace"
)

// version is set at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// nowUnix is a seam so run()'s trace timestamp is deterministic under test.
var nowUnix = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// newDockerExecutor is a seam: a per-task Docker sandbox in prod, overridden in tests
// so run() is exercised without a real daemon. Returns the executor + a cleanup func.
var newDockerExecutor = func(image, mount, mem string) (agent.Executor, func(), error) {
	box := sandbox.NewDockerSandbox(image, mount)
	box.Memory = mem
	if err := box.Start(); err != nil {
		return nil, nil, err
	}
	return box, box.Stop, nil
}

// run is the testable entrypoint: parse args, wire the client + executor, run the
// loop, and return a process exit code (0 solved, 1 failure, 2 usage error).
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("femto", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		model     = fs.String("model", "gpt-4o-mini", "model id")
		baseURL   = fs.String("base-url", "https://api.openai.com/v1", "OpenAI-compatible base URL (set to your vLLM/Ollama/NIM endpoint)")
		prompt    = fs.String("prompt", "", "task prompt (required)")
		executor  = fs.String("executor", "local", "tool executor: local|docker")
		category  = fs.String("category", "", "challenge category → picks the specialized sandbox image (docker only)")
		image     = fs.String("image", "", "override sandbox image (docker only)")
		mount     = fs.String("mount", "", "host dir bind-mounted at /task (docker only)")
		sandMem   = fs.String("sandbox-mem", "256m", "container RAM cap (docker only)")
		native    = fs.Bool("native", false, "native function-calling mode")
		maxSteps  = fs.Int("max-steps", 15, "max ReAct steps")
		temp      = fs.Float64("temp", 0.2, "sampling temperature")
		maxTokens = fs.Int("max-tokens", 1024, "max tokens per call")
		timeout   = fs.Duration("timeout", 120*time.Second, "per-call HTTP timeout")
		proxy     = fs.String("proxy", "", "HTTP CONNECT proxy for the LLM endpoint")
		jsonOut   = fs.Bool("json", false, "print the full result (incl. transcript) as JSON")
		tracePath = fs.String("trace", "", "append a self-describing JSONL trace here (for DB backfill)")
		runID     = fs.String("run-id", "", "run id stamped on the trace record")
		taskID    = fs.String("task-id", "", "task id stamped on the trace record")
		benchmark = fs.String("benchmark", "", "benchmark name stamped on the trace record")
		showVer   = fs.Bool("version", false, "print version and exit")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVer {
		fmt.Fprintln(stdout, "femto", version)
		return 0
	}
	if *prompt == "" {
		fmt.Fprintln(stderr, "femto: -prompt is required")
		return 2
	}
	key := firstEnv(getenv, "FEMTO_API_KEY", "OPENAI_API_KEY")
	if key == "" {
		fmt.Fprintln(stderr, "femto: set FEMTO_API_KEY (or OPENAI_API_KEY)")
		return 2
	}

	var ex agent.Executor
	if *executor == "docker" {
		img := *image
		if img == "" {
			img = sandbox.ImageForCategory(*category, nil, "")
		}
		box, cleanup, err := newDockerExecutor(img, *mount, *sandMem)
		if err != nil {
			fmt.Fprintln(stderr, "femto:", err)
			return 1
		}
		defer cleanup()
		ex = box
	} else {
		ex = sandbox.NewLocalExecutor("", 30*time.Second)
	}

	tools := agent.DefaultRegistry(ex)
	var schemas []any
	if *native {
		schemas = agent.Schemas(tools)
	}
	client, err := llm.New(*baseURL, key, *model, *temp, *maxTokens, *timeout, *proxy, schemas)
	if err != nil {
		fmt.Fprintln(stderr, "femto:", err)
		return 1
	}

	res := agent.RunAgent(*prompt, tools, client.Call, agent.Options{
		MaxSteps: *maxSteps, LLMRetries: 2, Native: *native,
	})

	if *tracePath != "" {
		if err := emitTrace(*tracePath, trace.Record{
			RunID: *runID, TaskID: *taskID, Benchmark: *benchmark, Category: *category,
			Model: *model, Native: *native, Temp: *temp, Prompt: *prompt,
		}, res); err != nil {
			fmt.Fprintln(stderr, "femto: trace:", err)
			return 1
		}
	}

	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	} else {
		fmt.Fprintf(stdout, "solved=%v steps=%d stop=%s llm_calls=%d\n",
			res.Solved, res.Steps, res.StopReason, res.LLMCalls)
		if res.Final != "" {
			fmt.Fprintln(stdout, "FINAL:", res.Final)
		}
	}
	if !res.Solved {
		return 1
	}
	return 0
}

// emitTrace appends one backfill-ready trace record for the run.
func emitTrace(path string, meta trace.Record, res agent.AgentResult) error {
	w, err := trace.Open(path)
	if err != nil {
		return err
	}
	defer w.Close()
	return w.Append(trace.FromResult(res, meta, nowUnix()))
}

func firstEnv(getenv func(string) string, keys ...string) string {
	for _, k := range keys {
		if v := getenv(k); v != "" {
			return v
		}
	}
	return ""
}
