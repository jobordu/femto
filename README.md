# femto

[![ci](https://github.com/jobordu/femto/actions/workflows/ci.yml/badge.svg)](https://github.com/jobordu/femto/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8.svg)](go.mod)

A **neutral, lightweight agent harness for honest LLM benchmarking.**

femto is a ~3 MB static Go binary (`FROM scratch`, **zero third-party dependencies**) that
drives any OpenAI-compatible model through a minimal think→act→observe loop over a
shell + python toolset. It is deliberately the *thinnest* agent a model can act through —
so an agentic benchmark measures the **model**, not the harness's cleverness.

```sh
go install github.com/jobordu/femto/cmd/femto@latest

export FEMTO_API_KEY=sk-...              # or OPENAI_API_KEY
femto -prompt "read /etc/os-release with the shell tool, then FINAL: <the ID>"
# solved=true steps=2 stop=final llm_calls=2

femto -base-url http://localhost:8000/v1 -model my-model -prompt "..."   # vLLM / Ollama / …
```

## Why femto exists

An agentic benchmark never measures the model alone — it measures the model *through a
harness*. A heavy harness (retry-on-failure, code-repair, re-planning, hint injection,
few-shot coaxing) **carries** weak models across the line and sometimes **fights** strong
ones. The scaffolding variance swamps the model variance, and results stop being
comparable across teams.

> A benchmark harness should be a **ruler, not a crutch.**
> If a model can't drive the minimal loop — emit a tool call, read the output, iterate,
> answer — there's nothing else to blame. The model is the variable.

femto is the minimal residue distilled from a predecessor harness that we ran for
100 k+ interactions, triaging failure-by-failure until its harness-fault rate reached
zero — then reduced to this dependency-free binary. We didn't *design* minimal; we
*reduced* to minimal and measured when it stopped mattering.

## Neutral — *it measures the model, not the harness*

- **No capability-substitution.** The harness relays; it doesn't reason. No retry, no
  code-repair, no re-planning, no hints, no few-shot. If the model writes a broken
  command it sees the error and must recover *itself*.
- **Format-tolerant, not capability-substituting.** It forgives *cosmetic* sloppiness
  (`bash`→`shell`, strips a ```` ``` ```` fence, reads inline `ACTION: x INPUT: y`) so it
  measures **capability**, not **format-compliance** — failing a model for a stray
  backtick is a hidden bias in the ruler. Forgiving format removes an unfair penalty; it
  adds **zero** help with the task.
- **Same surface for everyone.** Plain OpenAI-compatible HTTP — no per-vendor SDK, no
  model-specific prompt tuning. Every model acts through the identical thin loop.
- **Neutrality is measured, not asserted.** Every run emits a full trace, so each failure
  can be attributed `HARNESS` (our bug) / `MODEL` (fair) / `INFRA` (transient). The
  **harness-fault rate is the neutrality metric — the target, and the measurement, is 0.**

**Caveat, stated plainly:** neutral ≠ zero. The model needs *some* surface to act through.
femto makes that surface as thin as possible, format-tolerant so it's fair, then lets you
*verify* what's left doesn't confound.

## Lightweight — *the properties that make "neutral" verifiable*

Lightweight isn't vanity here — it's what lets a third party **audit and reproduce** the
neutrality. A harness you can't read, can't pin, and can't reproduce cannot credibly claim
to be neutral.

- **~3 MB static binary, zero deps** (stdlib only) → **auditable**: the loop is ~250 lines
  a reviewer reads in one sitting to confirm there is no hidden scaffolding.
- **A single versioned binary** → **reproducible**: pin `femto v0.1.0` and every lab and
  every paper runs the *identical* harness. Cross-model *and* cross-paper comparison
  becomes valid; heavy Python harnesses drift on every dependency bump, a static binary
  does not.
- **~5 ms cold start** (measured) → **negligible harness overhead** per interaction, so the
  instrument's footprint never distorts the run.
- **`FROM scratch`, cross-compiles anywhere** → the same bytes on a laptop, a CI runner, or
  a fleet node.

### Startup (measured)
Process start, macOS/arm64, median of 150 runs (`fork`+`exec` floor ≈ 1.6 ms):

| process | median |
|---|---|
| **femto** `-version` | **4.7 ms** |
| python interpreter (`-c pass`) | 20 ms |
| a python agent (import http client + modules) | 108 ms |
| `docker run` sandbox (cold) | 430–760 ms |

Note the sandbox is the real cold start (~100× the agent) — latency work belongs there (a
warm pool, or microVM snapshot-restore), not the agent.

## How it works

**Protocol.** Text mode (default), for models without reliable tool-calling:
```
ACTION: <tool>
INPUT:
<input>
```
…or `FINAL: <answer>` when done. `-native` switches to function-calling. Both survive the
quirks weak open models produce (inline `ACTION: x INPUT: y`, markdown-decorated markers,
harmony `<|channel|>` tool names, argv-as-JSON-array, …) — that is the *format-tolerance*,
not help with the task.

**Two containers.** femto (the brain) and the sandbox (the hands) are separate images.
The model's untrusted commands run in a disposable, **network-off** sandbox, category-routed
so each stays small:

| category | image | pull size |
|---|---|---|
| web, misc, shell | `ghcr.io/jobordu/femto-sbx-lite` | ~15 MB |
| crypto, ppc | `ghcr.io/jobordu/femto-sbx-crypto` | ~55 MB |
| pwn, reverse, forensics | `ghcr.io/jobordu/femto-sbx-pwn` | ~190 MB |
| untagged / unknown | `ghcr.io/jobordu/femto-sbx-full` | largest (safe fallback) |

```sh
femto -executor docker -category crypto -mount ./chal -prompt "solve the challenge in /task"
```
An unknown/blank category routes to `full` so a mis-tagged challenge never lands in a
sandbox missing its tools (network-off = can't install at runtime). The `femto` scratch
image is the brain only (no shell) — run it on a host for `-executor local`, or with the
docker socket mounted to orchestrate sandboxes.

**Traces.** `-trace runs.jsonl` appends one self-describing JSON line per run — model,
task, outcome, step count, llm-call count, full transcript — fsync'd and append-only. This
is what a per-model leaderboard and the HARNESS/MODEL/INFRA triage are built from.

## Build & release
```sh
make build      # static binary → bin/femto
make cross      # linux/{amd64,arm64} binaries
make test cover # tests + coverage (100%)
make sandboxes  # build the sandbox tiers locally (needs docker)
```
Tagging `vX.Y.Z` triggers CI (`.github/workflows/release.yml`): goreleaser binaries **and**
multi-arch container images → GHCR. Images are built by CI, never by hand.

## Layout
```
cmd/femto/         CLI entrypoint
internal/agent/    ReAct loop + text/native action parser + tool registry
internal/llm/      OpenAI-compatible client with 429/timeout backoff
internal/sandbox/  executors: local + docker, category→image routing
internal/trace/    self-describing JSONL trace writer
infra/sandbox/     specialized sandbox tiers (lite/crypto/pwn/full) + build.sh
```

## Contributing / Security / License
- [CONTRIBUTING.md](CONTRIBUTING.md) — zero-deps rule, tests required (100% coverage).
- [SECURITY.md](SECURITY.md) — intended use (authorized testing / CTF / research) and how
  to report a vulnerability.
- Licensed under [Apache-2.0](LICENSE).
