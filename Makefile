# One-box FaaS — build & ops entrypoints (spec §Commands).
# Go >= 1.24. One binary per cmd/ dir.
# (Bumped from 1.23: cmd/vmmd-stream-bridge uses the Go 1.24+
# http.Protocols API for H2C — srv.Protocols.SetUnencryptedHTTP2(true).
# go.mod pins 1.26.6; this comment is the floor for the toolchain
# so a developer on 1.23.x sees a clean compile error rather than
# a runtime panic. The 1.25.7 → 1.26.6 bump closes GO-2026-5942
# in golang.org/x/net/dns/dnsmessage (SVCB/HTTPS RR parser panic),
# fixed in net@go1.26.6.)

GO      ?= go
GOOS    ?= $(shell $(GO) env GOOS)
GOARCH  ?= $(shell $(GO) env GOARCH)
export GOOS GOARCH
PKGS    := ./...
COVERAGE_DIR := coverage
DAEMONS := apid gatewayd-public gatewayd-internal schedd vmmd vmmd-raw-bridge vmmd-stream-bridge builderd imaged meterd gregale githubd hostage-gen
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
# FAAS_FUNCTION_RUNNER_<RUNTIME_UPPER> — NODE22 / PYTHON312 / GO124 /
# GO124_ALPINE / NODE24 / PYTHON313 — to the resulting paths). Build matrix
# matches guest/init.
GUEST_RUNNERS := node22 python312 go124 node24 python313
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
	@$(MAKE) proto-versions-check
	@git diff --exit-code -- $(PROTO_ROOT) || (echo "generated *.pb.go is out of sync with .proto; run 'make proto' and commit the diff"; exit 1)
	@echo "proto-check: OK"

# proto-versions-check asserts the no-versions invariant post-regen:
# after `proto-normalize` strips the inner version lines, the
# `// versions:` block in every *.pb.go must be empty (only the header
# line itself, immediately followed by `// source: ...`). Pinned by
# PR #652 review finding M8; without this check a developer who
# re-runs `make proto` directly (skipping normalize) would commit
# `protoc-gen-go vX.Y.Z` lines that change with every toolchain bump
# and trip CI on the next PR. The check is a static assertion on
# checked-in files only — does NOT touch the toolchain — so it
# runs anywhere make(1) does.
.PHONY: proto-versions-check
proto-versions-check: ## Static gate: no protoc/protoc-gen-go version lines in any *.pb.go (PR #652 M8)
	@set -e; \
	  sentinel=$$(mktemp -u); \
	  trap 'rm -f "$$sentinel"' EXIT; \
	  find $(PROTO_ROOT) -name '*_grpc.pb.go' -o -name '*.pb.go' | while read f; do \
	    if grep -EH '^//[[:space:]]+(protoc-gen-go|protoc)[[:space:]]+v[0-9]' "$$f" >/dev/null; then \
	      echo "proto-versions-check: $$f contains a toolchain version line; run 'make proto-normalize' and commit the result" >&2; \
	      touch "$$sentinel"; \
	    fi; \
	  done; \
	  if [ -e "$$sentinel" ]; then exit 1; fi; \
	  echo "proto-versions-check: OK"

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

# DEPLOY-2 (issue #649 / ADR-078): pkg/daemonunit + pkg/daemonunitspec
# generate the systemd unit files for the 8 production daemons +
# faas-cp.slice + deploy/etc/daemons.json. The CI gate runs
# `make generate-check` on every PR; modifications to
# pkg/daemonunitspec/<daemon>.go require running `make generate`
# locally and committing the regenerated trees.
.PHONY: generate
generate: ## (re)generate systemd unit files + daemons.json from pkg/daemonunitspec (ADR-078)
	$(GO) run ./cmd/deployctl/ generate

.PHONY: generate-check
generate-check: ## CI gate: assert generated == committed for the 3 deploy trees + daemons.json
	$(GO) run ./cmd/deployctl/ check

.PHONY: generate-diff
generate-diff: ## Print drift between generated and committed (no exit 1)
	$(GO) run ./cmd/deployctl/ diff

.PHONY: test
test: grafana-mirror-check ## Unit tests — must pass on any machine, no KVM needed.
	# -timeout=18m: ./cmd/e2e under -race walks pkg/e2etest.buildApid
	# per test (unique -o path → cache miss). PR #541 (apply-time build
	# enqueue, ADR-068) added ~50 themed apply e2e tests, pushing the
	# cumulative cmd/e2e wall past the previous 15m ceiling on the
	# `unit tests` CI job. The dedicated `e2e (cmd/e2e, no metal)` job
	# also bumped to 20m for the same reason. Memory:
	# cmd-e2e-coverage-timeout-edge.md
	$(GO) test -race -count=1 -timeout=18m $(PKGS)

.PHONY: test-state-coverage
test-state-coverage: ## Assert pkg/state coverage ≥ 70% (excluding generated pkg/state/sqlc/**). Needs DATABASE_URL pointing at a reachable Postgres; PgStore tests skip cleanly otherwise.
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL not set — PgStore tests will skip; package total will only reflect MemStore coverage." ; )
	$(GO) test -race -count=1 -covermode=atomic -coverprofile=$(COVERAGE_DIR)/state.out ./pkg/state/...
	@$(MAKE) check-state-coverage COVERFILE=$(COVERAGE_DIR)/state.out

.PHONY: check-state-coverage
check-state-coverage: ## Assert pkg/state coverage ≥ 70% from existing profile (default: coverage/cover.out) without re-running tests
	@COVERFILE="$${COVERFILE:-$(COVERAGE_DIR)/cover.out}" ; \
	test -f "$$COVERFILE" || (echo "Coverage file $$COVERFILE not found — run tests with -coverprofile first" ; exit 1) ; \
	total=$$(awk '/^github\.com\/.*\/pkg\/state\// { \
		if ($$0 ~ /pkg\/state\/sqlc\//) next; \
		split($$0, a, " "); n=split(a[1], b, ":"); file=b[1]; \
		count=a[length(a)]+0; stmts=a[length(a)-1]+0; \
		tot_stmts += stmts; \
		if (count > 0) tot_hit += stmts; \
	} END { if (tot_stmts > 0) printf "%.1f", tot_hit*100/tot_stmts; else print "0.0" }' "$$COVERFILE") ; \
	awk -v t="$$total" 'BEGIN { exit (t+0 >= 70 ? 0 : 1) }' \
		&& echo "pkg/state coverage: $$total% ✓ (target ≥ 70%, excluding generated pkg/state/sqlc/**)" \
		|| (echo "pkg/state coverage: $$total% ✗ (target ≥ 70%, excluding generated pkg/state/sqlc/**)"; exit 1)


.PHONY: migrations-check
migrations-check: ## Static migration-contiguity check (no Postgres needed) — PR #93 follow-up
	$(GO) test -tags no_pg -race -count=1 -run 'TestMigrations' ./migrations/...

.PHONY: grafana-jq-check
grafana-jq-check: ## Validate every Grafana dashboard JSON parses cleanly (jq -e .). PR #837 (ADR-091 Amendment 1, issue #561) wired this into `test`.
	@for f in deploy/grafana/*.json deploy/ansible/roles/grafana/files/*.json; do \
	  if [ -f "$$f" ]; then \
	    jq -e . "$$f" > /dev/null || (echo "grafana-jq-check: parse failed $$f"; exit 1); \
	  fi; \
	done

.PHONY: grafana-mirror-check
grafana-mirror-check: ## SHA-256 byte-identity check for deploy/grafana/ → deploy/ansible/roles/grafana/files/ mirror. PR #837 (ADR-091 Amendment 1, issue #561) wired this into `test`.
	@for f in faas-fleet.json top-tenants.json top-throttled-apps.json edge-rules.json; do \
	  if [ -f "deploy/grafana/$$f" ] && [ -f "deploy/ansible/roles/grafana/files/$$f" ]; then \
	    a=$$(shasum -a 256 "deploy/grafana/$$f" | awk '{print $$1}'); \
	    b=$$(shasum -a 256 "deploy/ansible/roles/grafana/files/$$f" | awk '{print $$1}'); \
	    if [ "$$a" != "$$b" ]; then \
	      echo "grafana-mirror-check: $$f mismatch (deploy/grafana/ vs deploy/ansible/roles/grafana/files/)"; exit 1; \
	    fi; \
	  fi; \
	done

.PHONY: verify-secrets
verify-secrets: ## PR-P4: assert /etc/faas/sealed.env (or the file passed via SECRETS_FILE) is shaped correctly. CI runs this on every PR.
	@test -x deploy/scripts/verify-secrets.sh || (echo "deploy/scripts/verify-secrets.sh missing or not executable" ; exit 1)
	@SECRETS_FILE=$${SECRETS_FILE:-/etc/faas/sealed.env} bash deploy/scripts/verify-secrets.sh

.PHONY: hobby-route-audit
hobby-route-audit: ## Tier A (ADR-093): Hobby-tier app audit. Read-only harness that lists Hobby tenants, pulls /v1/apps/{slug}/routes, counts __route_other__ vs real-route entries. Exit 1 if any app is saturated. Needs FAAS_API_BASE + FAAS_TOKEN.
	@test -x deploy/scripts/adr093-hobby-audit.sh || (echo "deploy/scripts/adr093-hobby-audit.sh missing or not executable" ; exit 1)
	@bash deploy/scripts/adr093-hobby-audit.sh

.PHONY: test-load
test-load: ## Hot-path load test (1k rps, //go:build load) — spec §14 M4 row 2. Needs ≥ 2 vCPU.
	$(GO) test -tags=load -race -count=1 -v -timeout=10m ./pkg/gateway/...

.PHONY: gateway-bench
gateway-bench: ## Bench gatewayd-internal cold/hot/concurrent paths with -race; emits ns/op + allocs/op
	$(GO) test -race -bench=. -benchmem -run=^$ ./pkg/gateway/

.PHONY: test-metal
test-metal: ## Integration tests tagged //go:build metal — needs KVM + root
	$(GO) test -tags metal -race -count=1 $(PKGS)

.PHONY: leakcheck
leakcheck: ## Assert zero leaked netns/TAPs/jail uids/cgroups after tests
	@bash deploy/scripts/leakcheck.sh

.PHONY: e2e
e2e: ## End-to-end tests in cmd/e2e (needs Postgres reachable; metal subset via test-metal). Issue M7.
	@command -v psql >/dev/null 2>&1 || (echo "psql not on PATH; e2e needs DATABASE_URL set to a reachable Postgres" ; exit 1)
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL not set; set it to a reachable Postgres to run e2e" ; exit 1)
	# -timeout=20m: PR #541 added ~50 themed apply e2e tests, each
	# spawning `go build` for 7 daemons via pkg/e2etest.buildBinaries.
	# Cumulative wall time hit 15m on CI; 20m gives headroom for
	# reruns + cold-cache cold-runner edge cases.
	$(GO) test -race -count=1 -timeout=20m ./cmd/e2e/...

.PHONY: e2e-sandbox
e2e-sandbox: ## Live Paddle sandbox walk (operator-only; PR-P3). Reads secrets from secrets/.env.sandbox — NEVER committed.
	@test -f secrets/.env.sandbox || (echo "secrets/.env.sandbox missing; create it with FAAS_PADDLE_SANDBOX_API_KEY + FAAS_PADDLE_SANDBOX_WEBHOOK_SECRET from api.sandbox.paddle.com" ; exit 1)
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL not set; set it to a reachable Postgres to run e2e-sandbox" ; exit 1)
	# Operator-only: the test self-skips unless FAAS_PADDLE_SANDBOX_E2E=1
	# is exported. The build tag keeps the file out of `go test ./...`
	# CI runs. The secrets file is gitignored.
	FAAS_PADDLE_SANDBOX_E2E=1 $(GO) test -tags paddle_sandbox_e2e -race -count=1 -run=PaddleSandbox -timeout=10m ./cmd/e2e/...

.PHONY: doctor-paddle
doctor-paddle: ## PR-P4 operator smoke: run `gregale billing status --watch` for 60s + tail faas-apid journal for paddle_webhook.verify_failed lines. Operator-only.
	@test -x ./bin/gregale || (echo "./bin/gregale missing; run \`make build\` first" ; exit 1)
	@echo "→ Watching billing status for 60s (Ctrl-C to exit early)…"
	@timeout 60 ./bin/gregale billing status --watch || true
	@echo "→ Tailing faas-apid journal for paddle_webhook.verify_failed (last 5 min)…"
	@if command -v journalctl >/dev/null 2>&1; then \
		journalctl -u faas-apid --since "5 min ago" --no-pager | grep paddle_webhook.verify_failed || echo "no verify_failed lines in last 5 min"; \
	else \
		echo "journalctl not on PATH; on a Mac dev box, run \`make doctor-paddle\` on the actual control-plane node"; \
	fi

.PHONY: backup-pg
backup-pg: ## Take a Postgres base backup into /var/lib/pgsql/basebackup/basebackup-<UTC>/ (spec §14 M8)
	@test -d /var/lib/pgsql/basebackup || (echo "/var/lib/pgsql/basebackup missing — run the postgres role first" ; exit 1)
	@sudo -u postgres pg_basebackup -Ft -z -D /var/lib/pgsql/basebackup/basebackup-$$(date -u +%Y-%m-%dT%H%M%SZ) -P -X fetch --checkpoint=fast --label=faas-m8-nightly

.PHONY: backup-restore-drill
backup-restore-drill: ## Run the M8 restore drill end-to-end (must run on EX44 as root)
	sudo bash deploy/scripts/faas-m8-restore-drill.sh

.PHONY: lint-drill
lint-drill: ## Static lint of the restore drill script + record template shape (spec §14 M8)
	bash deploy/scripts/faas-m8-restore-drill_test.sh

.PHONY: backup-push-pg
backup-push-pg: ## Push the latest basebackup to Hetzner Storage Box (issue #250)
	@sudo systemctl start faas-pg-basebackup-push.service
	@sudo journalctl -u faas-pg-basebackup-push.service -n 50 --no-pager

.PHONY: backup-restore-verify
backup-restore-verify: ## T-7 throwaway restore verify on Hetzner Storage Box basebackup (issue #250)
	sudo bash deploy/scripts/pg-restore-verify.sh

.PHONY: lint-pg-restore-verify
lint-pg-restore-verify: ## Static lint of the off-host restore-verify script (issue #250)
	bash deploy/scripts/pg-restore-verify_test.sh

.PHONY: metal-lima
metal-lima: ## Run metal tests locally on an M3+ Mac via Lima nested KVM (see deploy/lima/README.md)
	@limactl list -q 2>/dev/null | grep -qx faas-metal || limactl start deploy/lima/faas-metal.yaml --tty=false
	limactl shell --workdir "$(CURDIR)" faas-metal sudo ./deploy/lima/run-metal.sh

.PHONY: metal-lima-m5
metal-lima-m5: ## Run the M5 §14 deploy-to-park cold-boot acceptance on Lima (subtest 1 only)
	@limactl list -q 2>/dev/null | grep -qx faas-metal || limactl start deploy/lima/faas-metal.yaml --tty=false
	limactl shell --workdir "$(CURDIR)" faas-metal sudo env RUN_TARGET=./cmd/e2e/ ./deploy/lima/run-metal.sh -run 'TestDeployWakeMetal/deploy-then-parked'

.PHONY: metal-soak
metal-soak: ## Issue #587 PR-A.8: 30-min mixed WS/HTTP/Upgrade drain soak on Lima (1-node). Verifies gateway_drain_wait_seconds histogram + gateway_inflight_requests gauge end-to-end. Pre-req: make metal-lima green.
	@limactl list -q 2>/dev/null | grep -qx faas-metal || { echo "faas-metal not started — run 'make metal-lima' first" >&2; exit 1; }
	limactl shell --workdir "$(CURDIR)" faas-metal sudo ./deploy/lima/run-metal-soak.sh

.PHONY: metal-lima-2node
metal-lima-2node: ## Tier A5 / ADR-066: two-node Lima fleet for the cross-node live-instance migration acceptance (§14 M9)
	@limactl list -q 2>/dev/null | grep -qx faas-metal || limactl start deploy/lima/faas-metal.yaml --tty=false
	@limactl list -q 2>/dev/null | grep -qx faas-metal-2b || limactl start deploy/lima/faas-metal-2node-b.yaml --tty=false
	limactl shell --workdir "$(CURDIR)" faas-metal sudo env FAAS_NODE_NAME=node-a ./deploy/lima/run-metal.sh
	limactl shell --workdir "$(CURDIR)" faas-metal-2b sudo env FAAS_NODE_NAME=node-b ./deploy/lima/run-metal.sh

.PHONY: ha-failover-drill
ha-failover-drill: ## Tier A8 / ADR-083: active-passive HA fail-over drill on the two-node Lima fleet (§14 M8)
	# Reuses the existing Tier A5 two-node fleet (faas-metal +
	# faas-metal-2b) — no separate 2node-ha.yaml config exists
	# (review finding #1: the previous target referenced a
	# config that wasn't in the repo). The drill itself is the
	# acceptance script + the manual operator steps in
	# docs/runbooks/active-passive-ha.md §Procedure; the
	# validation matrix in §Acceptance is what a green run
	# asserts. Pre-req: tier-A5 / ADR-066 two-node fleet
	# acceptance (make metal-lima-2node) must already be green.
	@limactl list -q 2>/dev/null | grep -qx faas-metal || { echo "faas-metal not started — run 'make metal-lima-2node' first" >&2; exit 1; }
	@limactl list -q 2>/dev/null | grep -qx faas-metal-2b || { echo "faas-metal-2b not started — run 'make metal-lima-2node' first" >&2; exit 1; }
	@bash -c 'set -e; \
	  echo "ha-failover-drill: see docs/runbooks/active-passive-ha.md for the 7-step procedure."; \
	  echo "  Step 1: deploy a hello app on node-A."; \
	  echo "  Step 2: psql: SELECT name, active FROM compute_nodes; — both active."; \
	  echo "  Step 3: limactl shell faas-metal sudo psql -c \"UPDATE compute_nodes SET active=false WHERE name=\$$(limactl shell faas-metal hostname)\""; \
	  echo "  Step 4: within HADNSRecordStaleSeconds=30s the failing node's StandbyState gauge hits 3 (draining);"; \
	  echo "          activePassiveFailoversTotal{outcome=\"dns_stale\"} bumps on the dying node (manual provider, review finding #14);"; \
	  echo "          the surviving node's StandbyState flips to 2 (warm) within HAStandbyWarmupIntervalMS."; \
	  echo "  Step 5: drain the FAAS_DNS_PROVIDER=manual curl from the dying node's stderr (the operator's job)."; \
	  echo "  Step 6: curl https://<app>.node-b.faas/ — must return 200 OK, latency \$$\le$$ 350 ms (Tier A5 budget)."; \
	  echo "  Step 7: limactl shell faas-metal-2b curl -s localhost:9100/metrics | grep active_passive_failovers_total — confirm dns_stale > 0."; \
	  exit 0'

.PHONY: lint-incompatible-mods
lint-incompatible-mods: ## CI: fail if any direct go.mod require is +incompatible
	@bash scripts/ci/check_no_incompatible_deps.sh

.PHONY: ha-write-redirect-drill
ha-write-redirect-drill: ## Tier A9 / ADR-089: standby write-redirect drill on the two-node Lima fleet (§14 M9)
	# Read-only drill (per ADR-089 §Open follow-ups): assumes the
	# active-passive topology is already configured (make
	# metal-lima-2node must be green; the prior tier-A8
	# ha-failover-drill must have run at least once).
	#
	# Pre-flights both Limas (faas-metal + faas-metal-2b); fails
	# closed if either is missing. The drill itself is the
	# acceptance script + the manual operator steps in
	# docs/runbooks/standby-write-redirect.md §Procedure; the
	# validation matrix in §Acceptance is what a green run
	# asserts. No UPDATE compute_nodes toggling — the drill
	# exercises the relay/redirect paths under steady-state
	# leader identity, not under failover.
	@limactl list -q 2>/dev/null | grep -qx faas-metal || { echo "faas-metal not started — run 'make metal-lima-2node' first" >&2; exit 1; }
	@limactl list -q 2>/dev/null | grep -qx faas-metal-2b || { echo "faas-metal-2b not started — run 'make metal-lima-2node' first" >&2; exit 1; }
	@bash -c 'set -e; \
	  echo "ha-write-redirect-drill: see docs/runbooks/standby-write-redirect.md for the 7-step procedure."; \
	  echo "  Pre-flight: both Limas running; mTLS cert material at /etc/faas/tls/gatewayd/egress-client.{crt,key}"; \
	  echo "              and CA at /etc/faas/tls/gatewayd/ca.crt on both boxes (ADR-052 keep set)."; \
	  echo "              Both boxes must run with FAAS_LEADER_REDIRECT_TLS_CERT set so the writeGate is"; \
	  echo "              constructed (the opt-in flag for the Tier A9 gate, ADR-089 §Decision #1)."; \
	  echo "  Step 1: read the active-passive decision from each box. The leader identity is refreshed"; \
	  echo "          on every compute_node_changed pg_notify event (Tier A8 / ADR-083):"; \
	  echo "          limactl shell faas-metal    curl -s localhost:9100/metrics | grep gateway_standby_state"; \
	  echo "          limactl shell faas-metal-2b curl -s localhost:9100/metrics | grep gateway_standby_state"; \
	  echo "          lex-min(name) is the leader (StandbyState=2/warm); the other is the standby."; \
	  echo "          limactl shell faas-metal    curl -s localhost:9100/metrics | grep gateway_standby_state"; \
	  echo "          limactl shell faas-metal-2b curl -s localhost:9100/metrics | grep gateway_standby_state"; \
	  echo "          lex-min(name) is the leader (StandbyState=2/warm); the other is the standby."; \
	  echo "  Step 2: capture baseline counter on the standby:"; \
	  echo "          limactl shell <standby> curl -s localhost:9100/metrics | grep gatewayd_internal_write_redirect_total"; \
	  echo "  Step 3: bearer write to the standby — should relay via mTLS to the leader and increment"; \
	  echo "          outcome=\"relayed\", auth_kind=\"bearer\" by exactly 1:"; \
	  echo "          limactl shell <standby> curl -H \"Authorization: Bearer <drill-token>\" -X POST \\"; \
	  echo "            -d \"{\\\"slug\\\":\\\"drill-<uuid>\\\",\\\"runtime\\\":\\\"node22\\\"}\" \\"; \
	  echo "            https://127.0.0.1:8080/v1/apps"; \
	  echo "  Step 4: cookie write to the standby — should 307-redirect to the leader and increment"; \
	  echo "          outcome=\"redirect_307\", auth_kind=\"cookie\" by exactly 1:"; \
	  echo "          limactl shell <standby> curl -i --cookie \"faas_sid=<drill-session>\" \\"; \
	  echo "            -X POST -d \"{\\\"slug\\\":\\\"drill-<uuid>\\\"}\" \\"; \
	  echo "            https://127.0.0.1:8080/v1/apps"; \
	  echo "  Step 5: verify both counters advanced and the 307 carries Retry-After: 5 + Location: https://<leader>/..."; \
	  echo "  Step 6: flip the leader via psql (operator-driven, NOT the drill) and re-run steps 3-4"; \
	  echo "          to confirm the counter vocabulary still drives with the new leader identity."; \
	  echo "  Step 7: limactl shell <standby> curl -s localhost:9100/metrics | grep gatewayd_internal_write_redirect_total \\"; \
	  echo "          — confirm relayed and redirect_307 both >= 1; same_box and leader_unreachable are 0."; \
	  echo "  The full read-only drill is automated by deploy/lima/run-ha-write-redirect.sh — exit codes 0/1/2/3/4"; \
	  echo "  mirror the Tier A8 ha-failover-drill. Run the script directly when the operator wants a non-"; \
	  echo "  interactive pass; the bash block above is the manual form."; \
	  exit 0'

.PHONY: lint
lint: egress-check lint-incompatible-mods ## golangci-lint via go tool (matches CI version v2.12.2) + egress artifact drift + +incompatible direct-dep gate
	@$(GO) tool golangci-lint run

.PHONY: scan
scan: ## Supply-chain scan: govulncheck (HIGH+) + Grype image scan + syft SBOM (issue #299)
	@command -v govulncheck >/dev/null 2>&1 || $(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	@command -v grype >/dev/null 2>&1 || { echo "grype required: https://github.com/anchore/grype/releases" >&2; exit 1; }
	@command -v syft >/dev/null 2>&1 || { echo "syft required: https://github.com/anchore/syft/releases" >&2; exit 1; }
	govulncheck -mode=source ./...
	@mkdir -p bin
	grype dir:images/ -o json --file bin/grype-results.json
	syft dir:. -o cyclonedx-json=bin/sbom.json --source-version "$$(git rev-parse --short HEAD)" --source-type directory

.PHONY: bootstrap
bootstrap: ## Idempotent single-box setup (ansible) — dev/lima. Back-compat for `make bootstrap` against 127.0.0.1.
	@test -f deploy/ansible/bootstrap.yml || (echo "deploy/ansible/bootstrap.yml not present yet (Gate-B PR-2)"; exit 1)
	ansible-playbook -i deploy/ansible/inventory/hosts.ini deploy/ansible/bootstrap.yml --limit box -e faas_box_role=single-box

.PHONY: bootstrap-control-plane
bootstrap-control-plane: ## Bootstrap fsn-1 (control-plane) — Gate-B PR-2
	@test -f deploy/ansible/bootstrap.yml || (echo "deploy/ansible/bootstrap.yml not present yet (Gate-B PR-2)"; exit 1)
	ansible-playbook -i deploy/ansible/inventory/hosts.ini deploy/ansible/bootstrap.yml --limit control_plane -e faas_box_role=control-plane

.PHONY: bootstrap-compute
bootstrap-compute: ## Bootstrap fsn-2 (compute-only) — Gate-B PR-2
	@test -f deploy/ansible/bootstrap.yml || (echo "deploy/ansible/bootstrap.yml not present yet (Gate-B PR-2)"; exit 1)
	ansible-playbook -i deploy/ansible/inventory/hosts.ini deploy/ansible/bootstrap.yml --limit compute_nodes -e faas_box_role=compute-only

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

# Egress policy (spec §11). Source of truth is pkg/netns/policy.go's
# HostPolicy.Render(). The Go-rendered artifact under
# deploy/ansible/roles/nftables/files/policy_nftables.conf is what
# `make bootstrap` ships to the host at /etc/nftables.conf when
# ansible_render=true (the default). With the ADR-055 Jinja2
# template active, ansible now DOES render the per-host file
# at bootstrap time, so the committed artifact is the canonical
# default-values render (the cross-check target below pins the
# two against each other).
#
# Per-host rendering (ADR-055): public_iface and masquerade_cidr
# are read from FAAS_PUBLIC_IFACE / FAAS_MASQUERADE_CIDR env vars
# (forwarded by cmd/faas-nft-render). The committed artifact uses
# the package defaults (eth0 + 10.100.0.0/16); a Hetzner compute
# node invokes the binary with the env overrides, captures the
# output, and the ansible template ships THAT to the host.
EGRESS_ARTIFACT := deploy/ansible/roles/nftables/files/policy_nftables.conf
EGRESS_JINJA2 := deploy/ansible/roles/nftables/files/policy_nftables.conf.j2

.PHONY: egress-render
egress-render: ## (re)generate the host nft ruleset artifact from pkg/netns/policy.go
	@mkdir -p $(dir $(EGRESS_ARTIFACT))
	@$(GO) run ./cmd/faas-nft-render > $(EGRESS_ARTIFACT)
	@echo "wrote $(EGRESS_ARTIFACT)"

# Cross-check (ADR-055 §Cross-check contract). The Go render is
# the source of truth; the Jinja2 template is a per-host
# verifier. Both must produce byte-identical output for the
# default values (eth0 + 10.100.0.0/16). A regression in either
# surface fails the build. CI runs this on every push.
#
# Trailing-newline normalization: Go's HostPolicy.Render() ends
# its output with a final `\n` (the closing brace of the
# `table inet faas` block); Jinja2 by default does not emit a
# trailing newline after the same `}`. Both are functionally
# equivalent when fed to `nft -c -f`, so the comparison normalizes
# trailing newlines on both sides to a single canonical form.
# This mirrors what cmd/e2e/sec11_sweep_test.go's
# TestSec11_PerHostEgressTemplating does.
.PHONY: egress-render-cross-check
egress-render-cross-check: ## Diff the Go render against the Jinja2 template render for default values
	@bash -c 'set -e; status=0; \
	  go_out=$$(go run ./cmd/faas-nft-render | python3 -c "import sys; sys.stdout.write(sys.stdin.read().rstrip(chr(10)) + chr(10))"); \
	  jinja_out=$$(python3 -c "from jinja2 import Template; print(Template(open(\"$(EGRESS_JINJA2)\").read()).render(public_iface=\"eth0\", masquerade_cidr=\"10.100.0.0/16\"), end=\"\")" | python3 -c "import sys; sys.stdout.write(sys.stdin.read().rstrip(chr(10)) + chr(10))"); \
	  if [ "$$go_out" != "$$jinja_out" ]; then \
	    echo "egress-render-cross-check: Go render and Jinja2 render DIVERGE for default values"; \
	    diff <(echo "$$go_out") <(echo "$$jinja_out") || true; \
	    status=1; \
	  else \
	    echo "egress-render-cross-check: Go render and Jinja2 render byte-identical for eth0/10.100.0.0/16"; \
	  fi; \
	  exit $$status'

# CI matrix (ADR-055): exercise the renderer for a non-default
# public_iface to confirm the substitution path works under the
# test rig. The Jinja2 template is rendered with the same value
# and the two MUST match. This is the load-bearing contract for
# a Hetzner compute node on `ens5`.
.PHONY: egress-render-matrix
egress-render-matrix: ## Render + cross-check for {eth0, ens5} public_iface variants
	@bash -c 'set -e; status=0; \
	  for iface in eth0 ens5; do \
	    for cidr in 10.100.0.0/16 10.101.0.0/16; do \
	      go_out=$$(FAAS_PUBLIC_IFACE=$$iface FAAS_MASQUERADE_CIDR=$$cidr go run ./cmd/faas-nft-render | python3 -c "import sys; sys.stdout.write(sys.stdin.read().rstrip(chr(10)) + chr(10))"); \
	      jinja_out=$$(python3 -c "from jinja2 import Template; print(Template(open(\"$(EGRESS_JINJA2)\").read()).render(public_iface=\"$$iface\", masquerade_cidr=\"$$cidr\"), end=\"\")" | python3 -c "import sys; sys.stdout.write(sys.stdin.read().rstrip(chr(10)) + chr(10))"); \
	      if [ "$$go_out" != "$$jinja_out" ]; then \
	        echo "egress-render-matrix: DIVERGE for iface=$$iface cidr=$$cidr"; \
	        diff <(echo "$$go_out") <(echo "$$jinja_out") || true; \
	        status=1; \
	      else \
	        echo "egress-render-matrix: OK iface=$$iface cidr=$$cidr"; \
	      fi; \
	    done; \
	  done; \
	  exit $$status'

.PHONY: egress-check
egress-check: egress-render-cross-check ## Cross-check the Go render against the Jinja2 template + nft -c -f if available + bridge-name guard test
	# Post-ADR-055: there is no committed static artifact any more —
	# the Jinja2 template `policy_nftables.conf.j2` is the
	# ansible-side verifier, the Go renderer is the source of truth,
	# and the byte-equality of the two surfaces for the default
	# values is what this gate enforces (delegated to
	# egress-render-cross-check via the prerequisite above).
	@bash -c 'set -e; status=0; \
	  if command -v nft >/dev/null 2>&1; then \
	    if go run ./cmd/faas-nft-render > /tmp/faas-egress-check.conf && nft -c -f /tmp/faas-egress-check.conf 2>/tmp/faas-egress.stderr; then \
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
# sqlc install path. CI drops the tarball at $$HOME/.local/sqlc/bin/sqlc
# (see .github/workflows/ci.yml `install sqlc` step); the same path is
# the local-dev convention so make sqlc-check works without a `go
# install` round-trip — which is necessary on Go < 1.26 because
# sqlc v1.31.1's go.mod requires go >= 1.26.0 and the ubuntu-latest
# runner is on Go 1.25.12 with GOTOOLCHAIN=local.
SQLC         ?= $(HOME)/.local/sqlc/bin/sqlc
# Bumped from v1.27.0 (IAM-3) — v1.27.0's pg_query_go cgo clashes with
# the macOS SDK strchrnul declaration and `go install` fails on this
# host. v1.31.1 ships a fixed pg_query_go. Generated output for the
# existing queries + the IAM-3 additions is byte-identical to what
# v1.27.0 would have produced (verified by sqlc-check passing).
SQLC_VER     ?= v1.31.1
# Mirror the CI workflow pin; never bump one without the other. The
# Makefile falls back to `go install` (where the host Go permits) when
# the tarball download is unreachable (offline dev box). The local
# path resolves to GOPATH/bin/sqlc on those hosts.
# Note: tag is /v$(SQLC_VER)/ (SQLC_VER already starts with v), but the
# asset name drops the `v` prefix (sqlc_1.31.1_linux_amd64.tar.gz, not
# sqlc_v1.31.1_...) — matches CI's SQLC_TARBALL="sqlc_${SQLC_VERSION}_linux_amd64.tar.gz"
# pattern (SQLC_VERSION="1.31.1", no v prefix).
SQLC_URL     ?= https://github.com/sqlc-dev/sqlc/releases/download/$(SQLC_VER)/sqlc_$(patsubst v%,%,$(SQLC_VER))_linux_amd64.tar.gz

.PHONY: sqlc
sqlc: ## Install sqlc at the pinned version (idempotent)
	@if command -v $(SQLC) >/dev/null 2>&1; then \
	  $(SQLC) version 2>&1 | grep -q $(SQLC_VER) && { echo "sqlc $(SQLC_VER) installed"; exit 0; }; \
	fi
	@mkdir -p "$(HOME)/.local/sqlc/bin"
	@tar_path="$$(mktemp)"; \
	if command -v curl >/dev/null 2>&1; then \
	  curl --fail --silent --show-error --location --output "$$tar_path" "$(SQLC_URL)" || { \
	    echo "make sqlc: curl download failed; falling back to go install" >&2; \
	    GOFLAGS='' GOBIN="$$(go env GOPATH)/bin" go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VER); \
	    exit 0; \
	  }; \
	else \
	  GOFLAGS='' GOBIN="$$(go env GOPATH)/bin" go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VER); \
	  exit 0; \
	fi; \
	tar --extract --gzip --file "$$tar_path" --directory "$$(dirname $$tar_path)"; \
	cp "$$(dirname $$tar_path)/sqlc" "$(HOME)/.local/sqlc/bin/sqlc" && chmod +x "$(HOME)/.local/sqlc/bin/sqlc"; \
	echo "sqlc $(SQLC_VER) installed at $(HOME)/.local/sqlc/bin/sqlc"

.PHONY: sqlc-generate
sqlc-generate: sqlc ## (re)generate pkg/state/sqlc/*.go from queries.sql + schema.sql
	$(SQLC) generate

.PHONY: sqlc-check
sqlc-check: sqlc ## CI gate: verify checked-in sqlc output matches what would be regenerated
	@set -e; tmp=$$(mktemp -d); \
	  trap 'rm -rf "$$tmp"' EXIT; \
	  mkdir -p "$$tmp/pkg/state"; \
	  cp sqlc.yaml schema.sql "$$tmp/"; \
	  cp pkg/state/queries.sql "$$tmp/pkg/state/"; \
	  (cd "$$tmp" && $(SQLC) generate); \
	  diff -r pkg/state/sqlc "$$tmp/pkg/state/sqlc" || \
	    { echo "sqlc-check: generated pkg/state/sqlc/*.go is out of sync with queries.sql or schema.sql; run 'make sqlc-generate' and commit the diff"; exit 1; }
	@echo "sqlc-check: OK"

.PHONY: migrate-up
migrate-up: ## Apply all pending migrations against $DATABASE_URL (idempotent)
	@command -v psql >/dev/null 2>&1 || (echo "psql not on PATH"; exit 1)
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL not set"; exit 1)
	@go run ./cmd/migrate

# schema.sql is the merged source-of-truth schema sqlc consumes. sqlc
# v1.27.0 does not merge `create table if not exists` statements across
# migration files, so pointing sqlc at migrations/ diverges from the live
# schema wherever a migration adds columns to an existing table. Idempotent:
# re-running produces byte-identical output (verified by the deterministic
# `pg_dump -s` output against an unchanged schema).
#
# The Go binary at cmd/schema-dump owns the flow: open pool, apply
# migrations via db.MigrateUp, shell to pg_dump -s, strip pg_dump version
# noise with compiled regexes (cmd/schema-dump/main_test.go::TestStripNoise).
# Putting it behind os/exec means failure modes (pg_dump missing, DSN
# invalid, regex mismatch) surface with explicit Go errors instead of
# opaque sed exit codes. DATABASE_URL must be set; pg_dump must be on
# PATH.
.PHONY: schema-dump
schema-dump: ## Regenerate schema.sql from a live Postgres (source of truth for sqlc)
	@command -v pg_dump >/dev/null 2>&1 || (echo "pg_dump not on PATH; install postgresql-client"; exit 1)
	@$(GO) run ./cmd/schema-dump -o schema.sql

# OpenAPI spec gate. The spec is the source of truth for documentation;
# the code is the source of truth for behavior. The gate is the bridge —
# `make spec-check` fails the PR if anything drifts.
#
# Mirrors the `proto-check` / `sqlc-check` / `egress-check` pattern: a
# checked-in artifact + a regenerate-and-diff verification. The vacuum
# binary is pinned via `go install …@v0.29.10` (same pattern as
# `protoc-gen-go@v1.36.11` and `sqlc@v1.27.0`).
#
# `pkg/apid/openapi.yaml` is a generated copy of `api/openapi.yaml` used
# by `//go:embed` (go:embed only resolves inside the package directory).
# The copy is checked in so the binary is self-contained at build time;
# `make spec-check` regenerates it AND asserts the working tree is clean
# so a missed copy fails CI rather than silently shipping stale bytes.
VACUUM     := $(or $(GOBIN),$(shell go env GOPATH)/bin)/vacuum
VACUUM_VER := v0.29.10
SPEC       := api/openapi.yaml
SPEC_EMBED := pkg/apid/openapi.yaml
VACUUM_RULES := api/vacuum.yaml

.PHONY: spec-install
spec-install: ## Install vacuum at the pinned version (idempotent)
	# CI installs vacuum in its own workflow step (ci.yml) and appends its
	# bin dir to $GITHUB_PATH, so `vacuum` resolves on PATH in the next
	# step. Locally, `make spec-check` first-run will fall through to
	# `go install` if vacuum isn't on PATH yet. Each line is its own shell
	# statement so this guard stays bash -e safe.
	@if command -v vacuum >/dev/null 2>&1; then \
	  vacuum version 2>&1 | grep -q $(VACUUM_VER) && { echo "vacuum $(VACUUM_VER) installed"; exit 0; }; \
	fi; \
	GOFLAGS='' GOBIN=$(or $(GOBIN),$(shell go env GOPATH)/bin) go install github.com/daveshanley/vacuum@$(VACUUM_VER)

.PHONY: spec-sync
spec-sync: ## Sync the //go:embed copy of the spec from api/openapi.yaml
	@cmp -s $(SPEC) $(SPEC_EMBED) || cp $(SPEC) $(SPEC_EMBED)
	@echo "spec-sync: $(SPEC_EMBED) matches $(SPEC)"

.PHONY: spec-lint
spec-lint: spec-install ## vacuum lint (style + rules) on the OpenAPI spec
	@vacuum lint -r $(VACUUM_RULES) $(SPEC)

.PHONY: spec-check
spec-check: spec-install spec-lint spec-sync denylist-md subprocessor-md ## CI gate: vacuum lint + AST parity + git clean + denylist.md + subprocessor.md drift (runs in PR CI)
	# No -race: the AST tests are pure CPU (no I/O, no goroutines). -race
	# would double the wall time without adding signal.
	@$(GO) test -count=1 -run TestSpecCompliance ./cmd/apid/...
	@git diff --exit-code -- $(SPEC) $(SPEC_EMBED) $(VACUUM_RULES) docs/denylist.md docs/compliance/subprocessors.md || \
	  (echo "spec-check: drift (spec, denylist.md, or subprocessor.md) — re-run 'make spec-check' or hand-fix to match"; exit 1)
	@echo "spec-check: OK"

.PHONY: images-lock-check
images-lock-check: ## CI gate: every images/*.Dockerfile FROM is digest-pinned via images/Dockerfile.lock (issue #197 B3.5 + B3.6)
	# Two pure-stdlib Python scripts (no install) run the gate:
	#   1. images_lock_check.py        — lock -> Dockerfile direction
	#                                    (entry exists, digest is real
	#                                    not REPLACE_ME, Dockerfile line
	#                                    matches `pinned_in_dockerfile`).
	#   2. audit_dockerfile_froms.py   — Dockerfile -> lock direction
	#                                    (every non-scratch FROM is
	#                                    either digest-pinned or covered
	#                                    by the lock; bare tags fail).
	# A failed images-lock-update leaves REPLACE_ME in the lock; the
	# gate stops the PR before it reaches main. Operator runs
	# `make images-lock-update` to resolve the real digests.
	@python3 scripts/ci/images_lock_check.py
	@python3 scripts/ci/audit_dockerfile_froms.py
	@echo "images-lock-check: OK"

.PHONY: images-lock-update
images-lock-update: ## Operator-only: resolve current registry digests, update Dockerfile.lock + FROM lines (issue #197 B3.5 + B3.6)
	# Skipped here on purpose — the resolver needs `crane` (or
	# `docker buildx imagetools inspect`) and registry credentials
	# the CI runner doesn't have. The operator runs the resolver
	# locally once at PR-merge time:
	#   python3 scripts/ci/images_lock_update.py --repo-root .
	# The script rewrites BOTH images/Dockerfile.lock (the source of
	# truth) and the matching `FROM ...@sha256:` line in each
	# Dockerfile. The CI gate above then accepts the PR.
	@echo "images-lock-update: not implemented in CI; run scripts/ci/images_lock_update.py locally"

.PHONY: denylist-md
denylist-md: ## Regenerate docs/denylist.md from the shared egress catalog (ADR-034 §Consequences)
	# Pure-Go generator — no template strings, no timestamps. Deterministic
	# order (v4 by prefix asc, v6 by prefix asc, SMTP by port asc) so the
	# git diff stays reviewable.
	@$(GO) run ./cmd/denylist-md > docs/denylist.md
	@echo "denylist-md: docs/denylist.md regenerated"

.PHONY: subprocessor-md
subprocessor-md: ## Regenerate docs/compliance/subprocessors.md from docs/compliance/subprocessors.json
	# Pure-Go generator — same shape as denylist-md. Enforces the DPA §7
	# 30-day notice window: any sub-processor entry with an effective_date
	# younger than notice_published_at + 30d fails the run. Order: by id
	# ascending so the diff stays reviewable.
	@$(GO) run ./cmd/subprocessor-md > docs/compliance/subprocessors.md
	@echo "subprocessor-md: docs/compliance/subprocessors.md regenerated"

.PHONY: sdk-check
sdk-check: ## CI gate: every OpenAPI route has a typed SDK method on pkg/api.Client
	# Pure-read AST/YAML diff (no I/O, no goroutines), so the recipe
	# mirrors spec-check's shape exactly. The script exits 1 when a
	# spec route has no SDK method; warnings about extra SDK helpers
	# (List*All, ExportAccount) are non-fatal so adding helpers
	# ahead of spec work isn't blocked.
	@$(GO) run ./cmd/sdk-coverage

.PHONY: sdk-gen-node
sdk-gen-node: ## Regenerate sdk/node/src/generated from api/openapi.yaml
	@cd sdk/node && npm run gen

.PHONY: sdk-gen-node-check
sdk-gen-node-check: ## Regenerate + assert clean diff (CI's `sdk-gen-node` job)
	@cd sdk/node && npm run gen:check

.PHONY: sdk-gen-node-twice
sdk-gen-node-twice: ## Determinism check: regen twice, must produce zero diff
	@cd sdk/node && npm run gen:check
	@cd sdk/node && npm run gen:check
	@echo "sdk-gen-node-twice: OK"

.PHONY: sdk-smoke-node
sdk-smoke-node: ## Build fakeapid fixture + run Node SDK smoke test
	@cd sdk/fakeapid && go build -o bin/fakeapid .
	@cd sdk/node && npm ci && npm run test:smoke

.PHONY: sdk-unit-node
sdk-unit-node: ## Run Node SDK unit tests (no fixture required)
	@cd sdk/node && npm ci && npm run test:unit

.PHONY: sdk-gen-python
sdk-gen-python: ## Regenerate sdk/python/faas_sdk from api/openapi.yaml
	@cd sdk/python && .venv/bin/python scripts/gen.py

.PHONY: sdk-gen-python-check
sdk-gen-python-check: ## Regenerate + assert clean diff (CI's `sdk-gen-python` job)
	@cd sdk/python && .venv/bin/python scripts/gen.py
	@git diff --exit-code -- sdk/python/faas_sdk/

.PHONY: sdk-gen-python-twice
sdk-gen-python-twice: ## Determinism check: regen twice, must produce zero diff
	@cd sdk/python && .venv/bin/python scripts/gen.py
	@cd sdk/python && .venv/bin/python scripts/gen.py
	@git diff --exit-code -- sdk/python/faas_sdk/
	@echo "sdk-gen-python-twice: OK"

.PHONY: sdk-gen
sdk-gen: ## (re)generate every generated SDK + assert clean diff vs HEAD
	# Aggregator for issue #266 PR 7. Composes the per-SDK `-check`
	# recipes (sdk-gen-{node,python}-check), each of which already
	# asserts `git diff --exit-code` against its own sub-tree. We do
	# NOT add a top-level `git diff --exit-code -- sdk/` here — it
	# would be redundant with the two per-SDK diffs and would mask
	# per-SDK attribution on failure. The Go SDK at sdk/go/ is
	# hand-written (extracted from pkg/api/ in PR 2); its spec-sync
	# invariant is covered by `make sdk-check`, a separate concern.
	@$(MAKE) sdk-gen-node-check
	@$(MAKE) sdk-gen-python-check
	@echo "sdk-gen: OK"

# Issue #745 (DEPLOY-PROV-9) pre-PR aggregator. Mirrors the
# required-status-check list enforced by the main branch ruleset
# (see docs/ci-required-checks.md). Local devs can run this before
# `git push` to surface drift without waiting 4-5 min for CI to
# evaluate. Does NOT cover jobs that require Postgres service
# containers (lint+build / unit-tests / e2e) — those run in CI only.
#
# Order matters: spec-check runs first because it catches the
# api/openapi.yaml ↔ pkg/apid/openapi.yaml drift that motivated the
# issue; sdk-gen runs last so a successful pre-pr implies every
# downstream artefact is in sync with the spec.
.PHONY: pre-pr
pre-pr: ## Pre-PR drift check: every regenerate-and-diff gate that runs in CI
	@echo "==> pre-pr: spec-check (api/openapi.yaml ↔ pkg/apid/openapi.yaml)"
	@$(MAKE) spec-check
	@echo "==> pre-pr: proto-check (checked-in *.pb.go matches protoc)"
	@$(MAKE) proto-check
	@echo "==> pre-pr: sqlc-check (checked-in sqlc output matches regenerated)"
	@$(MAKE) sqlc-check
	@echo "==> pre-pr: egress-check (nftables render + Go cross-check)"
	@$(MAKE) egress-check
	@echo "==> pre-pr: sdk-gen (node + python SDK regenerated, no diff)"
	@$(MAKE) sdk-gen
	@echo "pre-pr: OK (every drift gate clean)"

.PHONY: sdk-smoke-python
sdk-smoke-python: ## Build fakeapid fixture + run Python SDK smoke + unit tests
	@cd sdk/fakeapid && go build -o bin/fakeapid .
	@cd sdk/python && .venv/bin/python -m pip install --quiet -e .
	@cd sdk/python && .venv/bin/python -m pip install --quiet pytest pytest-asyncio
	@cd sdk/python && .venv/bin/python -m pytest tests/ --ignore=tests/debug_chain.py

.PHONY: sdk-unit-python
sdk-unit-python: ## Run Python SDK unit tests (no fixture required)
	@cd sdk/python && .venv/bin/python -m pytest tests/test_client.py tests/test_sse.py

