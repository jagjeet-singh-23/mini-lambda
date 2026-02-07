# Makefile

.PHONY: help migrate-up migrate-down migrate-create migrate-force db-reset fmt

# Database URL
DATABASE_URL := postgres://postgres:postgres@localhost:5432/mini_lambda?sslmode=disable

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

migrate-up: ## Run all migrations
	migrate -database $(DATABASE_URL) -path migrations up

migrate-down: ## Rollback last migration
	migrate -database $(DATABASE_URL) -path migrations down 1

migrate-create: ## Create a new migration (usage: make migrate-create NAME=add_users_table)
	migrate create -ext sql -dir migrations -seq $(NAME)

migrate-force: ## Force migration version (usage: make migrate-force VERSION=1)
	migrate -database $(DATABASE_URL) -path migrations force $(VERSION)

db-reset: ## Reset database (drop and recreate)
	migrate -database $(DATABASE_URL) -path migrations drop -f
	migrate -database $(DATABASE_URL) -path migrations up

db-status: ## Show current migration version
	migrate -database $(DATABASE_URL) -path migrations version

run: ## Run the application
	go run cmd/server/main.go

test: ## Run tests
	go test -v ./...

docker-up: ## Start docker services
	docker-compose up -d

docker-down: ## Stop docker services
	docker-compose down

docker-logs: ## View docker logs
	docker-compose logs -f

build: ## Build binary
	go build -o mini-lambda cmd/server/main.go

build-linux: ## Build binary for Linux (required for Docker)
	GOOS=linux GOARCH=amd64 go build -o mini-lambda-linux cmd/server/main.go

fmt: ## Format code (including 80-column line wrapping)
	go fmt ./...
	go install github.com/segmentio/golines@latest
	$(shell go env GOPATH)/bin/golines -w --max-len=80 .

# ============================================
# Microservices Build & Test Commands
# ============================================

build-services: ## Build all microservices
	@echo "Building gateway..."
	cd services/gateway && go build -o bin/gateway ./cmd
	@echo "Building lambda-service..."
	cd services/lambda-service && go build -o bin/lambda-service ./cmd
	@echo "Building build-service (master)..."
	cd services/build-service && go build -o bin/build-master ./cmd/master
	@echo "Building build-service (worker)..."
	cd services/build-service && go build -o bin/build-worker ./cmd/worker
	@echo "✅ All services built successfully!"

test-unit: ## Run unit tests for all services
	@echo "Running unit tests..."
	cd services/gateway && go test ./internal/... -v -short
	cd services/lambda-service && go test ./internal/... -v -short
	cd services/build-service && go test ./internal/... -v -short
	cd shared && go test ./... -v -short
	@echo "✅ Unit tests completed!"

test-integration: ## Run integration tests (requires infrastructure)
	@echo "Running integration tests..."
	cd infrastructure/docker && docker-compose up -d postgres redis rabbitmq minio
	@echo "Waiting for services..."
	sleep 10
	@echo "Running lambda-service integration tests..."
	cd services/lambda-service && go test ./internal/cache/... -v
	@echo "Running build-service integration tests..."
	cd services/build-service && go test ./internal/queue/... -v
	@echo "✅ Integration tests completed!"

test-e2e: ## Run end-to-end tests (requires all services)
	@echo "Running E2E tests..."
	cd infrastructure/docker && docker-compose up -d
	@echo "Waiting for services..."
	sleep 20
	cd tests/e2e && go test ./... -v -timeout 5m
	@echo "✅ E2E tests completed!"

test-all: test-unit test-integration test-e2e ## Run all tests
	@echo "✅ All tests completed!"

test-coverage: ## Generate test coverage reports
	@echo "Generating coverage..."
	cd services/gateway && go test ./... -coverprofile=coverage.out
	cd services/lambda-service && go test ./... -coverprofile=coverage.out
	cd services/build-service && go test ./... -coverprofile=coverage.out

docker-up-infra: ## Start only infrastructure services
	cd infrastructure/docker && docker-compose up -d postgres redis rabbitmq minio

docker-up-all: ## Start all services (infra + apps)
	cd infrastructure/docker && docker-compose up -d
	@echo "✅ All services started!"
	@echo "Gateway:        http://localhost:8080"
	@echo "Lambda Service: http://localhost:8081"
	@echo "Build Master:   http://localhost:8082"
	@echo "RabbitMQ UI:    http://localhost:15672"
	@echo "MinIO Console:  http://localhost:9001"

docker-clean: ## Clean all Docker resources
	cd infrastructure/docker && docker-compose down -v

clean-services: ## Clean build artifacts
	rm -rf services/gateway/bin
	rm -rf services/lambda-service/bin
	rm -rf services/build-service/bin
	rm -f services/*/coverage.out