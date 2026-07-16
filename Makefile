# One-box FaaS — build & ops entrypoints (spec §Commands).
# Go >= 1.23. One binary per cmd/ dir.

GO      ?= go
PKGS    := ./...
DAEMONS := apid gatewayd schedd vmmd builderd imaged meterd faas
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/onebox-faas/faas/pkg/wire.Version=$(VERSION)
BINDIR  := bin

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build every daemon into ./bin
	@mkdir -p $(BINDIR)
	@for d in $(DAEMONS); do \
	  echo "building $$d"; \
	  $(GO) build -ldflags '$(LDFLAGS)' -o $(BINDIR)/$$d ./cmd/$$d || exit 1; \
	done

.PHONY: test
test: ## Unit tests — must pass on any machine, no KVM needed
	$(GO) test -race -count=1 $(PKGS)

.PHONY: test-metal
test-metal: ## Integration tests tagged //go:build metal — needs KVM + root
	$(GO) test -tags metal -race -count=1 $(PKGS)

.PHONY: leakcheck
leakcheck: ## Assert zero leaked netns/TAPs/jail uids/cgroups after tests
	@bash deploy/scripts/leakcheck.sh

.PHONY: lint
lint: ## golangci-lint + custom checks
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || \
	  (echo "golangci-lint not installed; running go vet + gofmt check"; \
	   $(GO) vet $(PKGS); \
	   test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || \
	   (echo "gofmt: files need formatting"; gofmt -l $$(find . -name '*.go'); exit 1))

.PHONY: bootstrap
bootstrap: ## Idempotent host setup (ansible) — only on a dev EX44
	@test -f deploy/ansible/site.yml || (echo "deploy/ansible/site.yml not present yet (M0)"; exit 1)
	ansible-playbook -i deploy/ansible/inventory deploy/ansible/site.yml

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BINDIR)
