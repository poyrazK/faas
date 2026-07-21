# One-box FaaS — build & ops entrypoints (spec §Commands).
# Go >= 1.23. One binary per cmd/ dir.

GO      ?= go
PKGS    := ./...
DAEMONS := apid gatewayd schedd vmmd builderd imaged meterd faas githubd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/onebox-faas/faas/pkg/wire.Version=$(VERSION)
BINDIR  := bin

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: guest-runners ## Build every daemon + function runners into ./bin
	@mkdir -p $(BINDIR)
	@for d in $(DAEMONS); do \
	  echo "building $$d"; \
	  $(GO) build -ldflags '$(LDFLAGS)' -o $(BINDIR)/$$d ./cmd/$$d || exit 1; \
	done

# Function-runner shims live in the guest at /usr/local/bin/faas-runner and
# must be built for the guest architecture (linux/amd64, CGO off). Each
# shim is tiny (<1 MB); imaged stitches the matching one into the per-app
# ext4 when the deploy's runtime matches (cmd/imaged wires
# FAAS_FUNCTION_RUNNER_NODE22 / FAAS_FUNCTION_RUNNER_PYTHON312 to the
# resulting paths). Build matrix matches guest/init.
GUEST_RUNNERS := node22 python312
.PHONY: guest-runners
guest-runners: ## Build function-runner shims into ./bin/runners/<runtime>/faas-runner
	@mkdir -p $(BINDIR)/runners
	@for rt in $(GUEST_RUNNERS); do \
	  mkdir -p $(BINDIR)/runners/$$rt; \
	  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
	    $(GO) build -trimpath -o $(BINDIR)/runners/$$rt/faas-runner \
	      ./guest/runners/$$rt || exit 1; \
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

.PHONY: migrations-check
migrations-check: ## Static migration-contiguity check (no Postgres needed) — PR #93 follow-up
	$(GO) test -tags no_pg -race -count=1 -run 'TestMigrations' ./migrations/...

.PHONY: test-load
test-load: ## Hot-path load test (1k rps, //go:build load) — spec §14 M4 row 2. Needs ≥ 2 vCPU.
	$(GO) test -tags=load -race -count=1 -v -timeout=10m ./pkg/gateway/...

.PHONY: gateway-bench
gateway-bench: ## Bench gatewayd cold/hot/concurrent paths with -race; emits ns/op + allocs/op
	$(GO) test -race -bench=. -benchmem -run=^$ ./pkg/gateway/

.PHONY: test-metal
test-metal: ## Integration tests tagged //go:build metal — needs KVM + root
	$(GO) test -tags metal -race -count=1 $(PKGS)

.PHONY: leakcheck
leakcheck: ## Assert zero leaked netns/TAPs/jail uids/cgroups after tests
	@bash deploy/scripts/leakcheck.sh

.PHONY: metal-lima
metal-lima: ## Run metal tests locally on an M3+ Mac via Lima nested KVM (see deploy/lima/README.md)
	@limactl list -q 2>/dev/null | grep -qx faas-metal || limactl start deploy/lima/faas-metal.yaml --tty=false
	limactl shell --workdir "$(CURDIR)" faas-metal sudo ./deploy/lima/run-metal.sh

.PHONY: metal-lima-m5
metal-lima-m5: ## Run the M5 §14 deploy-to-park cold-boot acceptance on Lima (subtest 1 only)
	@limactl list -q 2>/dev/null | grep -qx faas-metal || limactl start deploy/lima/faas-metal.yaml --tty=false
	limactl shell --workdir "$(CURDIR)" faas-metal sudo env RUN_TARGET=./cmd/e2e/ ./deploy/lima/run-metal.sh -run 'TestDeployWakeMetal/deploy-then-parked'

.PHONY: lint
lint: egress-check ## golangci-lint via go tool (matches CI version v2.4.0) + egress artifact drift gate
	@$(GO) tool golangci-lint run

.PHONY: bootstrap
bootstrap: ## Idempotent host setup (ansible) — only on a dev EX44
	@test -f deploy/ansible/site.yml || (echo "deploy/ansible/site.yml not present yet (M0)"; exit 1)
	ansible-playbook -i deploy/ansible/inventory deploy/ansible/site.yml

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

# Egress policy (spec §11). Source of truth is pkg/netns/policy.go's
# HostPolicy.Render(). The artifact under deploy/ansible/roles/nftables/
# is what `make bootstrap` ships to the host at /etc/nftables.conf.
EGRESS_ARTIFACT := deploy/ansible/roles/nftables/files/policy_nftables.conf

.PHONY: egress-render
egress-render: ## (re)generate the host nft ruleset artifact from pkg/netns/policy.go
	@mkdir -p $(dir $(EGRESS_ARTIFACT))
	@$(GO) run ./cmd/faas-nft-render > $(EGRESS_ARTIFACT)
	@echo "wrote $(EGRESS_ARTIFACT)"

.PHONY: egress-check
egress-check: ## Verify the host nft artifact matches the live render + run nft -c -f if available + bridge-name guard test
	@bash -c 'set -e; status=0; \
	  out=$$(go run ./cmd/faas-nft-render); \
	  if [ "$$out" != "$$(cat $(EGRESS_ARTIFACT))" ]; then \
	    echo "egress-check: artifact drift — run \`make egress-render\` and commit the diff:"; \
	    diff <(echo "$$out") $(EGRESS_ARTIFACT) || true; \
	    status=1; \
	  else \
	    echo "egress-check: artifact matches live render"; \
	  fi; \
	  if command -v nft >/dev/null 2>&1; then \
	    if nft -c -f $(EGRESS_ARTIFACT) 2>/tmp/faas-egress.stderr; then \
	      echo "egress-check: nft -c -f OK"; \
	    else \
	      echo "egress-check: nft -c -f FAILED:"; \
	      cat /tmp/faas-egress.stderr; \
	      status=1; \
	    fi; \
	  else \
	    echo "egress-check: nft not on PATH; live kernel check skipped (macOS dev OK)"; \
	  fi; \
	  if go test -count=1 -run TestTenantBridgeMatches ./pkg/netns/... >/dev/null; then \
	    echo "egress-check: bridge-name guard OK"; \
	  else \
	    echo "egress-check: TestTenantBridgeMatches FAILED"; \
	    status=1; \
	  fi; \
	  exit $$status'

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BINDIR)

# M5+ Postgres/sqlc tooling. The pgx-backed Store applies migrations on
# startup (goose.SetBaseFS over migrations.FS); sqlc.yaml is committed for
# the day sqlc is available in the build environment (pganalyze/pg_query_go
# currently fails to compile on macOS SDKs — tracked separately).
SQLC     ?= $(GOBIN)/sqlc
SQLC_VER ?= v1.27.0

.PHONY: sqlc
sqlc: ## Install sqlc at the pinned version
	@command -v $(SQLC) >/dev/null 2>&1 && $(SQLC) version | grep -q $(SQLC_VER) && echo "sqlc $(SQLC_VER) already installed" && exit 0
	GOFLAGS='' go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VER)

.PHONY: sqlc-generate
sqlc-generate: sqlc ## (re)generate pkg/state/sqlc/*.sql.go from queries.sql
	$(SQLC) generate

.PHONY: sqlc-check
sqlc-check: ## Verify checked-in sqlc output matches what would be regenerated
	@$(MAKE) sqlc > /tmp/faas-sqlc-check.out 2>&1 || (cat /tmp/faas-sqlc-check.out; exit 1)
	@tmp=$$(mktemp -d) && cp -R pkg/state/queries.sql sqlc.yaml $$tmp/ && cd $$tmp && $(SQLC) generate >/dev/null 2>&1; \
	  diff -r pkg/state/sqlc $$tmp/pkg/state/sqlc >/dev/null || \
	    (echo "sqlc-check: generated sqlc/*.sql.go is out of sync with queries.sql; run 'make sqlc-generate' and commit the diff"; exit 1); \
	  rm -rf $$tmp
	@echo "sqlc-check: OK"

.PHONY: migrate-up
migrate-up: ## Apply all pending migrations against $DATABASE_URL (idempotent)
	@command -v psql >/dev/null 2>&1 || (echo "psql not on PATH"; exit 1)
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL not set"; exit 1)
	@go run ./cmd/migrate
