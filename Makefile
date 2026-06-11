.PHONY: up down dbs migrate server worker test test-ci vet fmt tidy build

COMPOSE := docker-compose -f deploy/docker-compose.dev.yml
DEV_DB  := postgres://lab:lab@localhost:5432/labdata?sslmode=disable

up: ## start local Postgres
	$(COMPOSE) up -d

down: ## tear down the compose stack
	$(COMPOSE) down

dbs: ## create the labdata + labdata_test databases (idempotent)
	@PG=$$(docker ps -q --filter name=postgres | head -1); \
	docker exec $$PG psql -U lab -d lab -c "CREATE DATABASE labdata;"      2>/dev/null || true; \
	docker exec $$PG psql -U lab -d lab -c "CREATE DATABASE labdata_test;" 2>/dev/null || true; \
	echo "labdata, labdata_test ready"

server: ## run the API server (migrates on boot) on :8080
	DATABASE_URL="$(DEV_DB)" go run ./cmd/server

worker: ## run the background job worker
	DATABASE_URL="$(DEV_DB)" go run ./cmd/worker

build: ## build all binaries
	go build ./...

vet: ## go vet everything
	go vet ./...

fmt: ## gofmt the tree
	gofmt -w internal/ cmd/ migrations/

test: ## go test ./... — parallel; each package auto-isolates its own test DB
	go test ./... -count=1

test-ci: ## CI variant: one shared test DB, serialized
	TEST_DATABASE_URL="postgres://lab:lab@localhost:5432/labdata_test?sslmode=disable" go test ./... -p 1

tidy:
	go mod tidy
