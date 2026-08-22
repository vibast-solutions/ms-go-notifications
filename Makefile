APP        := notifications
CMD        := ./cmd/notifications
BUILD_DIR  := build

.PHONY: build
build: ## Build the binary into build/
	go build -o $(BUILD_DIR)/$(APP) $(CMD)

.PHONY: run
run: ## Run the HTTP server (serve)
	go run $(CMD) serve

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: swagger
swagger: ## Regenerate swagger docs (placeholder until VIB-8 adds the first API)
	@echo "swagger generation arrives with the first API story (VIB-8)"

.PHONY: e2e-up
e2e-up: ## Start the e2e MySQL container
	docker compose -f docker-compose.e2e.yml up -d --wait mysql-e2e

.PHONY: e2e-down
e2e-down: ## Stop and remove the e2e containers
	docker compose -f docker-compose.e2e.yml down -v

.PHONY: e2e
e2e: e2e-up ## Run e2e tests (starts MySQL, runs tests, stops containers)
	go test -tags=e2e -v -count=1 ./e2e/ ; ret=$$? ; $(MAKE) e2e-down ; exit $$ret

.PHONY: migrate
migrate: ## Apply database migrations (uses DATABASE_DSN from env/.env)
	go run $(CMD) migrate

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR)

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
