# --- Build stage ---
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

# Cache dependencies
COPY server/go.mod server/go.sum ./server/
RUN cd server && go mod download

# Copy server source
COPY server/ ./server/

# Build binaries
ARG VERSION=dev
ARG COMMIT=unknown
RUN cd server && CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/server ./cmd/server
RUN cd server && CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" -o bin/multica ./cmd/multica
RUN cd server && CGO_ENABLED=0 go build -ldflags "-s -w" -o bin/migrate ./cmd/migrate

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /src/server/bin/server .
COPY --from=builder /src/server/bin/multica .
COPY --from=builder /src/server/bin/migrate .
COPY server/migrations/ ./migrations/
COPY docker/entrypoint.sh .
RUN sed -i 's/\r$//' entrypoint.sh && chmod +x entrypoint.sh

# Drop privileges: runtime as uid 1001 (audit F1, MUL-173).
# /app/data/uploads is created here so the named volume inherits app:app
# ownership on first mount. Pre-existing volumes from earlier root-only
# deploys must be chown'd once on the host (see deploy notes).
RUN addgroup -S app && adduser -S -G app -u 1001 app \
 && mkdir -p /app/data/uploads \
 && chown -R app:app /app
USER app

EXPOSE 8080

# Healthcheck (MUL-175 / F6). 127.0.0.1 (not localhost) avoids IPv6 resolution
# pitfalls when the server only binds IPv4. busybox wget ships with alpine.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -q --spider http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["./entrypoint.sh"]
