FROM node:22-alpine AS web-builder
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.23-alpine AS api-builder
WORKDIR /src
RUN apk add --no-cache build-base
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/emotion ./cmd/server

FROM alpine:3.21
RUN addgroup -S emotion && adduser -S -G emotion emotion
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=api-builder /out/emotion /app/emotion
COPY --from=web-builder /src/web/dist /app/web/dist
RUN mkdir -p /app/data && chown -R emotion:emotion /app
USER emotion
ENV PORT=8080 DATABASE_PATH=/app/data/emotion.db COOKIE_SECURE=true
EXPOSE 8080
VOLUME ["/app/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["/app/emotion"]
