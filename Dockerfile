# Build tools run natively; browser assets are architecture-independent.
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY api/ api/
COPY migrations/ migrations/
COPY --from=web /src/internal/controlplane/assets/console/ internal/controlplane/assets/console/
# Cross-compile instead of running the Go compiler under QEMU for ARM64.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath -ldflags='-s -w' -o /out/ \
    ./cmd/statusmon-api ./cmd/statusmond ./cmd/statusmon-admin ./cmd/statusmon-migrate

FROM debian:bookworm-slim AS runtime
LABEL org.opencontainers.image.source="https://github.com/Seeridia/StatusMon"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata curl && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /usr/local/bin/
USER 65532:65532
EXPOSE 8080
CMD ["statusmon-api"]
