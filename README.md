# femto

[![ci](https://github.com/jobordu/femto/actions/workflows/ci.yml/badge.svg)](https://github.com/jobordu/femto/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8.svg)](go.mod)

A **femto**-scale ReAct agent: a ~3 MB static Go binary (`FROM scratch` image, **zero
third-party dependencies**) that drives any OpenAI-compatible model through a
think→act→observe loop over a shell + python toolset — in text or native tool-calling
mode. Built for **fleet distribution**: tiny image, millisecond cold start, one
toolchain that cross-compiles to arm64 + amd64.

Ships with a set of **minimal, category-routed CTF/pentest sandbox images** so the agent
always runs untrusted, model-generated commands in a disposable, network-off container —
and each sandbox stays as small as the challenge category needs.

```sh
go install github.com/jobordu/femto/cmd/femto@latest

export FEMTO_API_KEY=sk-...              # or OPENAI_API_KEY
femto -prompt "What is 6 * 7? Reply FINAL: <n>"
# solved=true steps=1 stop=final llm_calls=1
# FINAL: 42
```

Point it at any OpenAI-compatible endpoint:
```sh
femto -base-url http://localhost:8000/v1 -model my-model -prompt "..."   # vLLM / Ollama / NIM / …
```

## Why Go
femto is JSON-over-HTTPS + a parser + a tool dispatcher. Go gives a ~3 MB static binary,
millisecond startup, a TLS stack that never surprises you, and trivial cross-compilation.
**Zero third-party deps** (stdlib only) — instant builds, nil supply chain.

| | image (pull) | binary start | deps |
|---|---|---|---|
| a comparable python agent | ~150 MB | ~110 ms | interpreter + libs |
| **femto** | **~3 MB scratch** | **~5 ms** | **none** |

### Startup (measured)
Process start, macOS/arm64, median of 150 runs (`fork`+`exec` floor ≈ 1.6 ms):

| process | median | note |
|---|---|---|
| **femto** `-version` | **4.7 ms** | Go runtime init + flag parse |
| python interpreter (`-c pass`) | 20 ms | bare, no imports |
| python agent (import http client + modules) | **108 ms** | the real per-invocation floor |
| `docker run` sandbox (lite) | **~430 ms** | container cold start |
| `docker run` sandbox (crypto) | **~760 ms** | container cold start |

Takeaway: femto's own start (~5 ms) is negligible — in a fleet the cold start is the
**sandbox** (~430–760 ms), ~100× heavier than the agent. So latency work belongs on the
sandbox side (a warm pool, or microVM snapshot-restore), not the agent.

## The two containers
femto (the **brain**) and the **sandbox** (the **hands**) are separate images. femto
stays tiny; the sandbox is where the model's commands run and is heavier because CTF
tooling is heavy — so it's **category-routed** to keep each one small:

| category | image | base | pull size |
|---|---|---|---|
| web, misc, shell | `ghcr.io/jobordu/femto-sbx-lite` | alpine, no Python | ~15 MB |
| crypto, ppc | `ghcr.io/jobordu/femto-sbx-crypto` | python-slim + crypto libs | ~55 MB |
| pwn, reverse, forensics | `ghcr.io/jobordu/femto-sbx-pwn` | + gdb/pwntools/RE tools | ~190 MB |
| untagged / unknown | `ghcr.io/jobordu/femto-sbx-full` | everything (safe fallback) | largest |

`internal/sandbox.ImageForCategory` picks the image from the challenge's category; an
unknown/blank category routes to `full` so a mis-tagged challenge never lands in a
sandbox missing its tools (network-off means it can't install at runtime).

```sh
femto -executor docker -category crypto -mount ./chal -prompt "Find the flag in /task"
```

> **Note:** the `femto` scratch image is the *brain* — it contains only the binary, no
> shell. Run it on a host for `-executor local`, or as an orchestrator with the docker
> socket mounted for `-executor docker`. The sandbox images are where commands execute.

## Protocol
Text mode (default), for models without reliable tool-calling:
```
ACTION: <tool>
INPUT:
<input>
```
…or `FINAL: <answer>` when done. `-native` switches to function-calling. Both survive the
quirks weak open models produce (inline `ACTION: x INPUT: y`, markdown-decorated markers,
harmony `<|channel|>` tool names, argv-as-JSON-array, …).

## Traces
`-trace runs.jsonl` appends one self-describing JSON line per run (model, task, outcome,
step count, llm-call cost proxy, full transcript) — fsync'd and append-only, ready to
load into a database.

## Build from source
```sh
make build      # static binary → bin/femto
make cross      # linux/{amd64,arm64} binaries
make test cover # tests + coverage (100%)
make image      # femto scratch image
make sandboxes  # build the sandbox tiers locally (needs docker)
```

## Releases & images
Tagging `vX.Y.Z` triggers CI (`.github/workflows/release.yml`), which builds the
cross-platform binaries (goreleaser) **and** all container images (multi-arch buildx) and
publishes them to GHCR — images are built by CI, never by hand.

## Layout
```
cmd/femto/         CLI entrypoint
internal/agent/    ReAct loop + text/native action parser + tool registry
internal/llm/      OpenAI-compatible client with 429/timeout backoff
internal/sandbox/  executors: local + docker, category→image routing
internal/trace/    backfill-ready JSONL trace writer
infra/sandbox/     specialized sandbox tiers (lite/crypto/pwn/full) + build.sh
```

## Contributing / Security / License
- [CONTRIBUTING.md](CONTRIBUTING.md) — zero-deps rule, tests required (100% coverage).
- [SECURITY.md](SECURITY.md) — intended use (authorized testing / CTF / research) and
  how to report a vulnerability.
- Licensed under [Apache-2.0](LICENSE).
