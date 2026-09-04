# CI Required Status Checks (issue #745 / DEPLOY-PROV-9)

This table is the **source of truth for which GitHub Actions jobs are
configured as required status checks on the `main` branch protection
ruleset (ID `19061133`)**. It exists so that renaming a job in
`.github/workflows/ci.yml` silently disables a merge gate is impossible:
the rename must update this file in the same PR.

GitHub matches status checks by exact `name:` string. The values in
the **Job name (exact)** column are the strings that must appear in
the `rules[]` of ruleset `19061133`. To verify the live state:

```
gh api repos/poyrazK/faas/rulesets/19061133
```

## Currently required

| Job name (exact)                          | Protects                                | Where in ci.yml       | Ruleset entry added |
|-------------------------------------------|-----------------------------------------|-----------------------|---------------------|
| `spec-check (OpenAPI lint + AST parity)`  | `api/openapi.yaml` ↔ `pkg/apid/openapi.yaml` drift (issue #745) | `ci.yml:426-481`      | 2026-08-08 / ADR-085 |

## Next up (planned, not yet required)

These CI jobs catch real drift but cannot be flipped to required until
the open PR backlog clears (audit 2026-08-08: #763, #762, #761, #754,
#753 were red on `lint + build`; #754 was also red on `CodeQL`; #753
was red on `unit tests (pg shard 2)`).

| Job name (exact)                                            | Protects                                  | Where in ci.yml   |
|-------------------------------------------------------------|-------------------------------------------|-------------------|
| `lint + build`                                              | golangci-lint v2.4.0 + gofmt repo-wide    | `ci.yml:70`       |
| `unit tests (pg shard 1 — apid/meter/migrations)`          | apid + meter + migrations + db + alerts   | `ci.yml:222`      |
| `unit tests (pg shard 2a — state/reconcile/reposcan)`       | pkg/state + reconcile + reposcan         | `ci.yml:461-545`  |
| `unit tests (pg shard 2b — gregale/gregalectl/daemons)`     | gregale + gregalectl + meterd + schedd    | `ci.yml:461-545`  |
| `unit tests (pure Go shard 1 — sched/fcvm/gateway)`        | sched + fcvm + gateway (-race)             | `ci.yml:~350`     |
| `unit tests (pure Go shard 2 — light packages)`            | the rest of the race-enabled tree         | `ci.yml:~380`     |
| `CodeQL`                                                    | CodeQL SARIF (security gate)              | `codeql.yml`      |
| `supply-chain-scan (govulncheck high+)`                     | Go vulnerability scan (HIGH+)             | `ci.yml:~900`     |
| `migrations (contiguity + apply)`                           | Migration slot races (issue #493+#496)    | `ci.yml:~160`     |
| `daemonunit-check (generated drift)`                        | `pkg/daemonunitspec/*.go` drift           | `ci.yml:~600`     |
| `sqlc-check (generated drift)`                              | sqlc query drift                          | `ci.yml:~440`     |
| `sdk-go build + test`                                       | sdk/go compilation + tests                | `ci.yml:~520`     |
| `sdk-node (gen-check + smoke + unit)`                       | sdk/node drift                            | `ci.yml:~540`     |
| `sdk-python (gen-check + smoke + unit)`                     | sdk/python drift                          | `ci.yml:~580`     |
| `proto-check`                                               | checked-in `*.pb.go` matches protoc       | `ci.yml:200-208`  |
| `load (1k rps hot-path)`                                    | p50 regression under load (issue #266)    | `ci.yml:~700`     |
| `workflow-lint (actionlint)`                                | Workflow YAML semantic lint               | `ci.yml:~1175`    |
| `runtime-contract-gate`                                     | Runtime image, source-artifact, adapter, and operator-doc contracts | `images.yml:runtime-contract-gate` |

Runtime OCI vulnerability scanning is enforced by the `images.yml` builder and
runtime matrix jobs. Those jobs scan the exact locally-built or published
artifacts; Dockerfile source text is not treated as an image scan.

`metal (KVM + root, manual)` is intentionally **not** a required
check — it is manual-only via `workflow_dispatch`.

## How to update this table

1. Rename the job in `.github/workflows/ci.yml`.
2. In the same PR, update the relevant row(s) in this file.
3. Apply the ruleset update via:

```
gh api -X PUT repos/poyrazK/faas/rulesets/19061133 --input <new-body>.json
```

4. Verify the new name is recognised:

```
gh api repos/poyrazK/faas/rulesets/19061133 | jq '.rules[] | select(.type=="required_status_checks")'
```

5. Open a throwaway drift PR to prove the new check actually gates merges.

## Local aggregator

`make pre-pr` runs the regenerate-and-diff subset of these checks
locally, in this order: `spec-check` → `proto-check` → `sqlc-check`
→ `egress-check` → `sdk-gen`. Does NOT cover CI-only jobs that need
Postgres service containers.

## `gregalectl` checks (issue #911 / ADR-110 PR-6.5)

The operator CLI (`cmd/gregalectl/`) is referenced by Makefile, 5
ansible roles, deployctl, and 4 e2e suites. CI pins its surface +
behaviour through these checks. None of these is currently required
on the ruleset (the `unit tests (pg shard N)` jobs cover them by
transitive invocation: every `_test.go` under `cmd/gregalectl/` runs
under one of the shards). They are listed here so a future rename or
split does not silently lose a load-bearing gate.

| Check                                          | Protects                                                                 | Where it lives                                        |
|------------------------------------------------|--------------------------------------------------------------------------|-------------------------------------------------------|
| `make build-clis`                              | Both `gregale` + `gregalectl` compile cleanly                            | `Makefile:16-19`                                      |
| `make manifest-ansible`                        | `gregalectl manifest ansible` renders the inventory + host_vars tree    | `Makefile:497-498`                                    |
| `make manifest-scale-check`                    | Generated 1/10/100/1000-node topology + Ansible check-mode gate       | `Makefile:534-535`                                    |
| `make metal-lima-splitbox`                     | End-to-end smoke: validate → render → release install → doctor --deep    | `Makefile:367`                                        |
| `commands_completion_test.go::TestCompletion_ManifestDrift` | `main.go` dispatcher ↔ `cli_meta.go` ↔ `commands_completion_test.go` tri-way drift | `cmd/gregalectl/commands_completion_test.go:23` |
| `json_parity_test.go::TestJSONOutputHonored`   | Every `cmdXxx` that references `jsonOutput`/`jsonEnabled` is exercised by a test (Tier A8.2) | `cmd/gregalectl/json_parity_test.go:38`     |
| `cmd/e2e/manifest_render_test.go`              | e2e golden path: manifest validate → render → daemon reload              | `cmd/e2e/manifest_render_test.go`                     |
| `cmd/e2e/release_install_test.go`              | e2e golden path: release bundle → install → UPSERT compute_nodes        | `cmd/e2e/release_install_test.go`                     |
| `cmd/e2e/image_role_mutation_test.go`          | e2e golden path: PR-B role mutation (drain → mutate → UPSERT)           | `cmd/e2e/image_role_mutation_test.go`                 |
| `cmd/e2e/doctor_test.go`                       | e2e golden path: doctor finds expected drift findings on poisoned fixtures | `cmd/e2e/doctor_test.go`                         |

When a future PR adds a new top-level command to `gregalectl`, the
`TestCompletion_ManifestDrift` guard catches missing dispatch ↔
manifest entries immediately. When a new `--json` arm is added, the
`json_parity_test` extractor catches a missing `_test.go` exercise of
that arm immediately. Both gates are intentional tripwires, not
primary enforcement — the primary enforcement is the unit-test
shards' failure when the new code does not compile or test.
