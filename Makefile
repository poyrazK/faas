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

# M1 gRPC codegen (ADR-013). Generated *.pb.go is COMMITTED — do not run
# `make proto` to produce output; CI uses `proto-check` to verify drift only.
PROTO_ROOT := api/proto
GOBIN     ?= $(shell go env GOPATH)/bin
PROTOC     ?= protoc
PROTOC_GO  ?= $(GOBIN)/protoc-gen-go
PROTOC_GRPC ?= $(GOBIN)/protoc-gen-go-grpc
PROTOS     := $(shell find $(PROTO_ROOT) -name '*.proto' 2>/dev/null)

.PHONY: proto
proto: ## (re)generate *.pb.go from .proto (local toolchain: protoc-gen-go, protoc-gen-go-grpc in $GOBIN)
	@command -v protoc >/dev/null 2>&1 || (echo "protoc not on PATH; install with 'brew install protobuf'"; exit 1)
	@test -x "$(PROTOC_GO)" || (echo "protoc-gen-go not in $$GOBIN; install with 'go install google.golang.org/protobuf/cmd/protoc-gen-go@latest'"; exit 1)
	@test -x "$(PROTOC_GRPC)" || (echo "protoc-gen-go-grpc not in $$GOBIN; install with 'go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest'"; exit 1)
	@for p in $(PROTOS); do \
	  echo "protoc $$p"; \
	  PATH="$(GOBIN):$$PATH" $(PROTOC) --proto_path=$(PROTO_ROOT) --go_out=$(PROTO_ROOT) --go_opt=paths=source_relative \
	    --go-grpc_out=$(PROTO_ROOT) --go-grpc_opt=paths=source_relative \
	    $$p || exit 1; \
	done

.PHONY: proto-check
proto-check: ## Verify checked-in *.pb.go matches what protoc would emit (ignoring toolchain version comments)
	@$(MAKE) proto-normalize > /tmp/faas-proto-check.out 2>&1 || (cat /tmp/faas-proto-check.out; exit 1)
	@git diff --exit-code -- $(PROTO_ROOT) || (echo "generated *.pb.go is out of sync with .proto; run 'make proto' and commit the diff"; exit 1)
	@echo "proto-check: OK"

# proto runs codegen then strips the toolchain-version comments
# (// 	protoc-gen-go v..., // 	protoc v...) from every *.pb.go before
# exiting. The wire bytes protoc produces are unaffected; we just don't
# want a patched protoc version to fail CI.
.PHONY: proto-normalize
proto-normalize: proto
	@find $(PROTO_ROOT) -name '*_grpc.pb.go' -o -name '*.pb.go' | while read f; do \
	  sed -i.bak -E \
	    -e '/^\/\/.*protoc(-gen-go(-grpc)?)?[ \t]+v[0-9]+\.[0-9]+\.[0-9]+( \([^)]+\))?[[:space:]]*$$/d' \
	    "$$f" && rm -f "$$f.bak"; \
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
lint: ## golangci-lint if installed, else go vet + gofmt check
	@if command -v golangci-lint >/dev/null 2>&1; then \
	  golangci-lint run; \
	else \
	  echo "golangci-lint not installed; running go vet + gofmt check"; \
	  $(GO) vet $(PKGS); \
	  test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || \
	    (echo "gofmt: files need formatting"; gofmt -l $$(find . -name '*.go'); exit 1); \
	fi

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
