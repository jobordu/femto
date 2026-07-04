# Contributing to femto

Thanks for your interest! femto aims to stay **tiny, dependency-free, and legible** —
contributions should preserve those properties.

## Ground rules

- **Zero third-party Go dependencies.** femto is stdlib-only; a `require` block in
  `go.mod` will be rejected. If you think you need a dependency, open an issue first.
- **Tests required.** New code needs tests; the suite is at 100% statement coverage and
  CI enforces `go test ./...`. Run `make cover` locally.
- **Keep it small.** The agent is a loop + a parser + a dispatcher. Resist scope creep
  in the core; larger features (new executors, loaders) belong behind clean seams.

## Development

```sh
make test       # go test ./...
make cover      # coverage summary (aim: 100%)
make vet        # go vet
make build      # static binary → bin/femto
make sandboxes  # build the local sandbox tiers (needs docker)
```

## Pull requests

1. Fork and branch from `main`.
2. `gofmt` your code (`make fmt`) and make sure `make test vet` passes.
3. Keep commits focused; write a clear message explaining the *why*.
4. By contributing, you agree your contributions are licensed under Apache-2.0.

## Reporting bugs / ideas

Open an issue with a minimal repro (for parser bugs: the exact model output that
mis-parsed). Security issues: see [SECURITY.md](SECURITY.md) — report privately.
