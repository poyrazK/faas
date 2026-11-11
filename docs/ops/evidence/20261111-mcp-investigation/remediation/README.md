# MCP remediation verification — 2026-11-11

All 21 findings in [the issue log](../../../mcp-investigation-findings.md) have
local fixes on `fix/mcp-investigation-backlog`. The parent directory retains the
original investigation logs; this directory contains passing post-fix evidence.
No deployment or live provider call was performed.

These results describe the original local fix batch. See
[the current-main integration verification](../integration/README.md) for the
final cron receipt policy and the expanded Node/Python suite results after
integrating main for PR #7080.

## Results

| Check | Result | Evidence |
| --- | --- | --- |
| Retained reproduction probes, all five rounds | All nine command groups exit zero | `probe_results.json` and corresponding `*_repros.log` |
| Management, diagnostic client, hosted bridge, daemon units and Go starter suites | Passed, including PostgreSQL worker tests | `go_packages.log` |
| Go ownership/revocation and cron retry/concurrent-writer controls | Passed | `go_authorization_recovery.log` |
| Conflicting cron retry and saved receipt both request inspection | Passed | `go_final_recovery.log` |
| JSON/SSE persistence failure withholds uncommitted bytes | Passed | `go_commit_failure.log` |
| Source guards and Go resource subscriptions after static-check cleanup | Passed | `go_final_source_subscriptions.log` |
| Affected MCP CLI checks | Passed | `go_cli.log` |
| CLI resource/catalog watches, fleet pins and plugin tool references | Passed | `go_cli_consumers.log` |
| Full Node starter suite with PostgreSQL | 155 passed, zero skipped | `node_suite.log` |
| Full Python starter suite with PostgreSQL | 52 passed, zero skipped | `python_suite.log` |
| Go vet, affected packages | Passed | `go_vet.log` (empty on success) |
| Go vet after final cron recovery change | Passed | `go_final_vet.log` (empty on success) |
| golangci-lint, affected Go packages | Passed, zero issues | `golangci_lint.log` |
| Repository test-store, seal-domain, problem-detail and trimpath checks | Passed | `teststorecheck.log`, `sealdomaincheck.log`, `problemdetailcheck.log`, `trimpath_check.log` (empty on success) |

Permanent regression tests live alongside the affected packages and starter
tests. The retained Go overlays add separately named probes for independent
reproduction. The two Node lifecycle probes wait for readiness and the claim
handoff explicitly so their assertions also work after the lifecycle fixes.

## Commands and prerequisites

Use Go 1.26.10. This managed workspace used its downloaded toolchain with
`GOPATH=/workspace/faas-env/go`,
`GOCACHE=/workspace/faas-env/cache/go-build`, and `GOMAXPROCS=4`.
`GOMEMLIMIT=6GiB` bounds larger CLI/static-analysis builds.

Set `MCP_TASKS_TEST_DATABASE_URL` to a disposable PostgreSQL database whose
test role can create and drop isolated schemas; no production database was
used. Set `MCP_TASKS_REQUIRE_POSTGRES=1` for the full starter suites. These runs
used a private loopback PostgreSQL 17 instance. Database URLs and credentials
are omitted from the retained manifest.

From the repository root:

```sh
go test -trimpath -gcflags=all=-dwarf=false -p=1 \
  ./pkg/mcpmanagement ./pkg/mcphosting ./cmd/mcp-bridged \
  ./pkg/daemonunitspec ./cmd/gregale/templates/mcp-go -count=1 -timeout=240s

go test -trimpath -gcflags=all=-dwarf=false -p=1 ./cmd/gregale \
  -run '^Test(MCP|CmdMCP)' -count=1 -timeout=240s

go test -trimpath -gcflags=all=-dwarf=false -p=1 ./cmd/gregale \
  -run '^Test(ClaudePluginManifestAndMCPServer|AgentFacingMCP|ProbeMCP|ValidateMCP|ReadMCP|RunMCP|RecordMCP)' \
  -count=1 -timeout=120s

go vet -trimpath -gcflags=all=-dwarf=false -p=1 \
  ./pkg/mcpmanagement ./pkg/mcphosting ./cmd/mcp-bridged \
  ./cmd/gregale/templates/mcp-go ./cmd/gregale

go tool golangci-lint run --allow-serial-runners --timeout=15m \
  ./pkg/mcpmanagement ./pkg/mcphosting ./cmd/mcp-bridged \
  ./cmd/gregale/templates/mcp-go ./cmd/gregale

python3 docs/ops/evidence/20261111-mcp-investigation/run_probes.py \
  --node-starter /absolute/path/to/temporary-node-starter \
  --output-dir docs/ops/evidence/20261111-mcp-investigation/remediation
```

Install pinned Node dependencies into a temporary starter copy outside the
embedded repository template tree. Run `npm test` there. From the Python
starter directory, run `python3 -m unittest discover -q` with its pinned OAuth
and PostgreSQL dependencies installed. In this workspace, PostgreSQL Python
dependencies were installed under a temporary path and supplied via
`PYTHONPATH`; no embedded dependency tree was added to the repository.

Static repository checks use `go run ./scripts/ci/teststorecheck -base HEAD`,
`go run ./scripts/ci/sealdomaincheck`,
`go run ./scripts/ci/problemdetailcheck`, and
`bash scripts/ci/check_trimpath_safe.sh /absolute/path/to/repository`.
For the lint run, `GOFLAGS='-trimpath -gcflags=all=-dwarf=false -p=1'`
and `GOLANGCI_LINT_CACHE=/workspace/faas-env/cache/golangci-lint` selected the
shared build configuration and a writable cache.

Live provider OAuth and native KVM acceptance remain outside this local test
scope. The 47-second heartbeat regression is a focused sustained-queue check,
not a throughput qualification.
