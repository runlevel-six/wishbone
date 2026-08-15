TEMPL_VERSION := v0.3.1020
IMAGE ?= wishd:latest

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: tools
tools: ## Install the templ compiler
	go install github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)

.PHONY: generate
generate: ## Compile .templ files to Go
	templ generate

.PHONY: build
build: generate ## Build the static binary
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/wishd ./cmd/wishd

.PHONY: run
run: generate ## Run locally against ./tmp (http, no TLS)
	WISHD_ADDR=127.0.0.1:8080 \
	WISHD_DATA_DIR=./tmp \
	WISHD_SECURE_COOKIES=false \
	WISHD_BOOTSTRAP_ADMIN=$${USER:-admin} \
	WISHD_BOOTSTRAP_ADMIN_PASSWORD=$${WISHD_DEV_PASSWORD:-changemechangeme} \
	go run ./cmd/wishd

.PHONY: test
test: generate ## Run the whole test suite
	go test ./...

.PHONY: race
race: generate ## Run the tests with the race detector (claim concurrency)
	go test -race ./...

.PHONY: cover
cover: generate ## Test with a coverage summary
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: vet
vet: generate ## go vet
	go vet ./...

.PHONY: fmt
fmt: ## gofmt the tree
	gofmt -w $$(git ls-files '*.go' | grep -v '_templ.go')

.PHONY: check
check: fmt vet test ## Everything CI would run

.PHONY: image
image: ## Build the container image
	docker build -t $(IMAGE) .

.PHONY: clean
clean:
	rm -rf bin coverage.out tmp
