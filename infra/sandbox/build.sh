#!/usr/bin/env bash
# Build femto's specialized sandbox tiers. Each is category-routed by the agent
# (internal/sandbox/exec.go:ImageForCategory): lite for web/misc/shell (no Python,
# tiny), crypto for crypto/ppc, pwn for pwn/reverse/forensics, full for untagged/
# multi-category (safe fallback).
#
#   ./build.sh              # all tiers, host-native arch
#   ./build.sh lite crypto  # only the named tiers
#   PLATFORM=linux/amd64 ./build.sh   # force an arch (emulated if not host)
#   WITH_SYMPY=1 ./build.sh crypto full
set -euo pipefail
cd "$(dirname "$0")"

TIERS=("$@"); [ ${#TIERS[@]} -eq 0 ] && TIERS=(lite crypto pwn full)
PLATFORM="${PLATFORM:-}"
plat=(); [ -n "$PLATFORM" ] && plat=(--platform "$PLATFORM")
sympy=(--build-arg "WITH_SYMPY=${WITH_SYMPY:-0}")

for tier in "${TIERS[@]}"; do
    df="Dockerfile.${tier}"
    [ -f "$df" ] || { echo "no $df" >&2; exit 1; }
    echo "==> building femto-sbx-${tier}:latest"
    docker build "${plat[@]}" "${sympy[@]}" -f "$df" -t "femto-sbx-${tier}:latest" .
done

echo "==> tiers:"
docker images 'femto-sbx-*' --format '    {{.Repository}}:{{.Tag}}  {{.Size}}'
