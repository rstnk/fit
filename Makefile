PROJECT     := fit
MAIN        := ./cmd/fit
INSTALL_DIR ?= $(HOME)/.local/bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.Version=$(VERSION)

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into bin/
	go build -ldflags '$(LDFLAGS)' -o bin/$(PROJECT) $(MAIN)

.PHONY: run
run: ## Run fit (pass args with ARGS=...)
	go run $(MAIN) $(ARGS)

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: lint
lint: ## Vet and staticcheck
	go vet ./...
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed, skipping"

.PHONY: fmt
fmt: ## Format the source
	go fmt ./...

.PHONY: modernize
modernize: ## Apply Go modernisations
	go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@latest -fix ./...

.PHONY: tidy
tidy: ## Tidy go.mod
	go mod tidy

.PHONY: deps
deps: ## Check the external binaries fit needs
	@for b in ffmpeg ffprobe magick; do \
		command -v $$b >/dev/null 2>&1 && echo "  ok      $$b" || echo "  missing $$b"; \
	done
	@ffmpeg -hide_banner -filters 2>/dev/null | grep -q zscale \
		&& echo "  ok      zscale (HDR tonemapping available)" \
		|| echo "  missing zscale (ffmpeg built without libzimg, HDR inputs will be refused)"

.PHONY: install
install: ## Install fit into ~/.local/bin (override with INSTALL_DIR=)
	@mkdir -p $(INSTALL_DIR)
	GOBIN=$(INSTALL_DIR) go install -ldflags '$(LDFLAGS)' $(MAIN)
	@echo "run 'make completions' for zsh tab completion"

.PHONY: completions
completions: ## Install the zsh completion into ~/.zfunc
	@mkdir -p $(HOME)/.zfunc
	cp completions/_fit $(HOME)/.zfunc/_fit
	@echo "add 'fpath=(~/.zfunc $$fpath)' before compinit in ~/.zshrc"

.PHONY: clean
clean: ## Remove build output
	rm -rf bin/
