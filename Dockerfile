# gomoku-battle all-in-one image: vite frontend -> embedded static dir,
# go backend serving HTTP+WS on :8080 (PORT), /debug/pprof on PPROF=1,
# RedisStore on REDIS_ADDR (graceful in-memory fallback when unreachable).

# --- frontend build
FROM node:20-bookworm-slim AS web
WORKDIR /build
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# --- backend build
FROM golang:1.27-bookworm AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download
COPY backend/ ./backend/
RUN cd backend && CGO_ENABLED=0 go build -o /out/server ./cmd/server

# --- runtime
FROM debian:bookworm-slim
RUN useradd -r -u 10001 app
WORKDIR /app
COPY --from=api /out/server /app/server
COPY --from=web /build/dist /app/frontend/dist
USER app
ENV PORT=8080
EXPOSE 8080
# optional: PPROF=1, REDIS_ADDR=host:6379, ALLOW_ANY_ORIGIN=1
CMD ["/app/server"]
