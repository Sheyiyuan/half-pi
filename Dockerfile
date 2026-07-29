# ── Half-Pi Mind Docker image ──
# Multi-stage build with embedded WebUI

# Stage 1: Build WebUI frontend
FROM node:24-alpine AS webui
WORKDIR /src/webui
COPY webui/package.json webui/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY webui/ ./
RUN pnpm check && pnpm build

# Stage 2: Build Go binary
FROM golang:1.25-alpine AS builder
WORKDIR /src

# Install build deps
RUN apk add --no-cache gcc musl-dev

# Copy go modules
COPY go.work go.work.sum ./
COPY modules/gateway-core/go.mod modules/gateway-core/go.sum ./modules/gateway-core/
COPY modules/half-pi-core/go.mod modules/half-pi-core/go.sum ./modules/half-pi-core/
COPY modules/half-pi-face/go.mod modules/half-pi-face/go.sum ./modules/half-pi-face/
COPY modules/half-pi-hand/go.mod modules/half-pi-hand/go.sum ./modules/half-pi-hand/
COPY modules/half-pi-mind/go.mod modules/half-pi-mind/go.sum ./modules/half-pi-mind/

# Download deps
RUN cd modules/gateway-core && go mod download && \
    cd ../half-pi-core && go mod download && \
    cd ../half-pi-face && go mod download && \
    cd ../half-pi-hand && go mod download && \
    cd ../half-pi-mind && go mod download

# Copy source
COPY modules/ ./modules/

# Copy WebUI dist into embed location
COPY --from=webui /src/webui/dist modules/half-pi-mind/cmd/half-pi-mind/webui/

# Build binaries
RUN cd modules/half-pi-mind && go build -o /out/half-pi-mind ./cmd/half-pi-mind/ && \
    cd ../half-pi-face && go build -o /out/half-pi-face ./cmd/half-pi-face/ && \
    cd ../half-pi-hand && go build -o /out/half-pi-hand ./cmd/half-pi-hand/

# Stage 3: Minimal runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -h /home/half-pi half-pi

COPY --from=builder /out/* /usr/local/bin/

USER half-pi
WORKDIR /home/half-pi

EXPOSE 15707
ENV HALF_PI_HOME=/home/half-pi/.half-pi

ENTRYPOINT ["half-pi-mind"]
CMD []
