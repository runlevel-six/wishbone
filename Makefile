TEMPL_VERSION := v0.3.1020
IMAGE ?= wishbone:latest
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# templ compiles the .templ files to Go. Prefer one already on PATH; otherwise
# fall back to the Go bin directory and install it on demand, so a clean
# checkout can run `make check` without a PATH detour first.
GOBIN := $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN := $(shell go env GOPATH)/bin
endif
TEMPL := $(shell command -v templ 2>/dev/null || echo $(GOBIN)/templ)

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: tools
tools: ## Install (or reinstall) the templ compiler
	go install github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)

$(TEMPL):
	go install github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)

.PHONY: generate
generate: $(TEMPL) ## Compile .templ files to Go
	$(TEMPL) generate

.PHONY: build
build: generate ## Build the static binary
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/wishbone ./cmd/wishbone

.PHONY: run
run: generate ## Run locally against ./tmp (http, no TLS)
	WISHBONE_ADDR=127.0.0.1:8080 \
	WISHBONE_DATA_DIR=./tmp \
	WISHBONE_SECURE_COOKIES=false \
	WISHBONE_BOOTSTRAP_ADMIN=$${USER:-admin} \
	WISHBONE_BOOTSTRAP_ADMIN_PASSWORD=$${WISHBONE_DEV_PASSWORD:-changemechangeme} \
	go run ./cmd/wishbone

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
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

.PHONY: clean
clean:
	rm -rf bin coverage.out tmp
