# femto agent → a ~8MB scratch image. Multi-stage: build a fully-static, stripped
# binary, then copy it (plus CA certs for TLS to the LLM endpoint) into scratch.
# Cross-arch: `docker buildx build --platform linux/arm64,linux/amd64` — Go cross-
# compiles natively, no QEMU needed for the compile step.
# Build on the runner's native arch and cross-compile to the target (Go needs no QEMU),
# so multi-arch image builds stay fast.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
# cache deps first (none today — stdlib only — but keeps the layer cache stable)
COPY go.mod ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
        go build -trimpath -ldflags='-s -w' -o /out/femto ./cmd/femto

FROM scratch
# CA bundle so crypto/tls can verify the LLM endpoint's cert.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/femto /femto
ENTRYPOINT ["/femto"]
