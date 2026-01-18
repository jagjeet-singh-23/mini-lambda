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

fmt: ## Format code (including 80-column line wrapping)
	go fmt ./...
	go install github.com/segmentio/golines@latest
	$(shell go env GOPATH)/bin/golines -w --max-len=80 .