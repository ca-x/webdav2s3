# ── Build Arguments ────────────────────────────────────────────────────────
ARG GOLANG_VERSION=1.23
ARG ALPINE_VERSION=3.21

# ── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:${GOLANG_VERSION}-alpine AS builder

# Build args from GitHub Actions
ARG VERSION=dev
ARG BUILDTIME
ARG GITCOMMIT

RUN apk add --no-cache git ca-certificates tzdata gcc musl-dev

WORKDIR /build

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build with CGO for SQLite
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build \
      -ldflags="-s -w \
        -extldflags '-static'" \
      -o /out/webdav2s3 \
      ./cmd/server

# ── Stage 2: Minimal runtime ─────────────────────────────────────────────────
FROM alpine:${ALPINE_VERSION}

LABEL org.opencontainers.image.source="https://github.com/czyt/webdav2s3"
LABEL org.opencontainers.image.description="WebDAV2S3 - Multi-backend WebDAV server"
LABEL org.opencontainers.image.licenses="MIT"

RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S webdav \
    && adduser -S -G webdav webdav \
    && mkdir -p /app/data \
    && chown -R webdav:webdav /app

COPY --from=builder /out/webdav2s3 /app/webdav2s3

WORKDIR /app
USER webdav

EXPOSE 8080

# Persistent volumes
VOLUME ["/app/data"]

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/webdav2s3"]
CMD []