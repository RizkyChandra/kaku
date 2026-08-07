.PHONY: help dev build test lint generate css sqlc fmt clean tools docker
.DEFAULT_GOAL := help

TAILWIND_VERSION := v4.3.3
BIN := bin
TAILWIND := $(BIN)/tailwindcss
CSS_IN := internal/web/tailwind.css
CSS_OUT := internal/web/static/tailwind.css

UNAME_S := $(shell uname -s | tr A-Z a-z)
UNAME_M := $(shell uname -m)
TAILWIND_OS := $(if $(filter darwin,$(UNAME_S)),macos,linux)
TAILWIND_ARCH := $(if $(filter arm64 aarch64,$(UNAME_M)),arm64,x64)

help: ## Show this help
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*##/\t/' | expand -t20

$(TAILWIND):
	@mkdir -p $(BIN)
	curl -sfL -o $@ https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$(TAILWIND_OS)-$(TAILWIND_ARCH)
	chmod +x $@

css: $(TAILWIND) ## Build the stylesheet
	@test -f $(CSS_IN) && $(TAILWIND) -i $(CSS_IN) -o $(CSS_OUT) --minify || echo "no $(CSS_IN) yet, skipping"

watch-css: $(TAILWIND)
	$(TAILWIND) -i $(CSS_IN) -o $(CSS_OUT) --watch

sqlc: ## Regenerate database code
	@test -f sqlc.yaml && go tool sqlc generate || echo "no sqlc.yaml yet, skipping"

templ: ## Regenerate templates
	go tool templ generate

generate: templ sqlc css ## Regenerate everything

build: generate ## Build the binary
	CGO_ENABLED=0 go build -o $(BIN)/kaku ./cmd/kaku

test: ## Run tests
	go test ./...

lint: ## Vet and check generated files are current
	go vet ./...
	gofmt -l .

fmt: ## Format
	gofmt -w .
	go tool templ fmt .

dev: generate ## Run fakes3 + the server with .env loaded
	@test -f .env || cp .env.example .env
	@test -d cmd/fakes3 && (go run ./cmd/fakes3 &) || true
	set -a; . ./.env; set +a; go run ./cmd/kaku

docker: ## Build the container image
	docker build -t kaku:dev .

clean:
	rm -rf $(BIN) dist data
