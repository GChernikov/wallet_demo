.PHONY: up down build docker-build seed test lint loadtest

up:
	docker compose up -d

down:
	docker compose down

build:
	go build ./...

docker-build:
	docker compose build

# Seed load test wallets (run after docker compose up)
seed:
	go run ./cmd/seed

test:
	go test -race ./...

lint:
	golangci-lint run ./...

# Run a load test scenario. Usage: make loadtest SCENARIO=750-wallets-4rps-sfu
loadtest:
	k6 run loadtest/$(SCENARIO).js
