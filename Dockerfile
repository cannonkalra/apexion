# syntax=docker/dockerfile:1

# ---- builder -------------------------------------------------------------
# CGO is required by the embedded DuckDB driver. The templ *_templ.go files and
# the compiled Tailwind app.css are committed to the repo, so the image build
# does not need the templ or tailwind toolchains.
FROM golang:1.26-bookworm AS builder

ENV CGO_ENABLED=1
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags "-s -w" -o /out/apexion ./cmd/apexion \
 && go build -trimpath -ldflags "-s -w" -o /out/seed    ./cmd/seed

# ---- runtime -------------------------------------------------------------
FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates libstdc++6 curl \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/apexion /usr/local/bin/apexion
COPY --from=builder /out/seed    /usr/local/bin/seed
COPY configs/apexion.yaml /app/configs/apexion.yaml

# Assets and migrations are embedded in the binary; nothing else to copy.
RUN mkdir -p /app/data
ENV APEXION_STORAGE_PATH=/app/data/apexion.duckdb

EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --retries=5 \
  CMD curl -fsS http://localhost:8080/healthz || exit 1

ENTRYPOINT ["apexion"]
CMD ["serve", "-c", "/app/configs/apexion.yaml"]
