#!/usr/bin/env bash
# Push femto + the sandbox tiers to GHCR so the fleet can pull them.
#
# One-time auth (the gh OAuth token lacks write:packages by default):
#   gh auth refresh -h github.com -s write:packages
#   gh auth token | docker login ghcr.io -u <you> --password-stdin
#
# Then:
#   ./infra/push-ghcr.sh                 # push femto + lite/crypto/pwn/full
#   NS=jobordu ./infra/push-ghcr.sh      # to a personal namespace instead of the org
#   IMAGES="femto sbx-lite" ./infra/push-ghcr.sh   # a subset
#
# NOTE: images are single-arch (whatever the daemon built). For a multi-arch fleet,
# build+push with buildx: `docker buildx build --platform linux/amd64,linux/arm64 --push`.
set -euo pipefail
cd "$(dirname "$0")/.."

NS="${NS:-jobordu}"
TAG="${TAG:-latest}"
REG="ghcr.io/${NS}"
IMAGES="${IMAGES:-femto sbx-lite sbx-crypto sbx-pwn sbx-full}"

for img in $IMAGES; do
    # local tag: femto:latest, or femto-sbx-<tier>:latest
    local_tag="${img/sbx-/femto-sbx-}:${TAG}"
    [ "$img" = "femto" ] && local_tag="femto:${TAG}"
    remote="${REG}/${img}:${TAG}"
    if ! docker image inspect "$local_tag" >/dev/null 2>&1; then
        echo "!! skip $img — $local_tag not built (run: make image / make sandboxes)"
        continue
    fi
    echo "==> $local_tag -> $remote"
    docker tag "$local_tag" "$remote"
    docker push "$remote"
done
echo "==> done. Pull: docker pull ${REG}/femto:${TAG}"
