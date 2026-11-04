.PHONY: dev test build

dev:
	go run ./cmd/server

test:
	go test ./...

build:
	cd web && npm ci && npm run build
	go build -o bin/emotion ./cmd/server
