FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY api/ api/
COPY migrations/ migrations/
COPY --from=web /src/internal/controlplane/assets/console/ internal/controlplane/assets/console/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/statusmon-api ./cmd/statusmon-api && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/statusmond ./cmd/statusmond && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/statusmon-admin ./cmd/statusmon-admin && \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/statusmon-migrate ./cmd/statusmon-migrate

FROM debian:bookworm-slim AS runtime
LABEL org.opencontainers.image.source="https://github.com/Seeridia/vendor-status-monitoring"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata curl && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /usr/local/bin/
USER 65532:65532
EXPOSE 8080
CMD ["statusmon-api"]
