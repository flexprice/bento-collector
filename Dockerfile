# syntax=docker/dockerfile:1.4
# Multi-stage build for Bento Flexprice
#
# Stage 1: Build the Go binary.
#
# The builder is pinned to BUILDPLATFORM (the native arch of the machine doing
# the build) and cross-compiles to TARGETARCH. Without this, buildx runs the
# whole non-native build under QEMU emulation — and because main.go imports
# bento/public/components/all (~3000 packages), an emulated arm64 build took
# 40+ minutes in CI. Go cross-compiles natively, so this costs nothing.
ARG GO_VERSION=1.26.5
ARG ALPINE_VERSION=3.23
FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# Provided automatically by buildx
ARG TARGETOS
ARG TARGETARCH

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy source code
COPY . .

# Build the binary.
#
# `-a` (force-rebuild everything, including the stdlib) was removed: it defeats
# the build cache on every run for no benefit here. The cache mounts below let
# repeat builds reuse compiled packages.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-w -s" -trimpath -o bento-flexprice main.go

# Stage 2: Create minimal runtime image
FROM alpine:${ALPINE_VERSION}

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1000 bento && \
    adduser -D -u 1000 -G bento bento

# Set working directory
WORKDIR /app

# Copy binary + configs, owned by bento at copy time (avoids a duplicate
# chown -R layer that would double these files on disk).
COPY --from=builder --chown=bento:bento /build/bento-flexprice /app/bento-flexprice
COPY --from=builder --chown=bento:bento /build/examples /app/examples
# internal/ holds prod pipelines referenced via -c /app/internal/...
COPY --from=builder --chown=bento:bento /build/internal /app/internal

# Switch to non-root user
USER bento

# Expose Bento HTTP server port
EXPOSE 4195

# Set entrypoint
ENTRYPOINT ["/app/bento-flexprice"]

# Default command - use kafka config for production
CMD ["-c", "/app/examples/dummy-events-to-flexprice.yaml"]

