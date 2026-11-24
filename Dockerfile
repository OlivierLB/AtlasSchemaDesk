FROM node:24-slim AS frontend
WORKDIR /build/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend ./
RUN npm run build

FROM golang:1.27 AS backend
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
COPY --from=frontend /build/web/dist/app ./web/dist/app
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/sulcata ./cmd/sulcata

# A shell stays available for trusted restore_command entries; no Node or Python.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* && useradd --create-home --uid 10001 sulcata
COPY --from=backend /out/sulcata /usr/local/bin/sulcata
COPY LICENSE /usr/share/doc/sulcata/LICENSE
USER sulcata
WORKDIR /workspace
EXPOSE 8765
ENTRYPOINT ["sulcata", "--config", "/workspace/sulcata.toml", "--bind", "0.0.0.0"]
