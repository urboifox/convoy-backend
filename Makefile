.PHONY: dev run build tidy db-up db-down db-reset

dev:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

tidy:
	go mod tidy

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

db-reset:
	docker compose down -v && docker compose up -d postgres
