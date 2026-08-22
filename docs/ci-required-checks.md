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
| `unit tests (pg shard 2 — state/gregale/schedd)`            | pkg/state + cmd/gregale + schedd          | `ci.yml:~290`     |
| `unit tests (pure Go shard 1 — sched/fcvm/gateway)`        | sched + fcvm + gateway (-race)             | `ci.yml:~350`     |
| `unit tests (pure Go shard 2 — light packages)`            | the rest of the race-enabled tree         | `ci.yml:~380`     |
| `CodeQL`                                                    | CodeQL SARIF (security gate)              | `codeql.yml`      |
| `image-scan (Grype + govulncheck high+)`                    | Vulnerability scan (Grype + govulncheck)   | `ci.yml:~900`     |
| `migrations (contiguity + apply)`                           | Migration slot races (issue #493+#496)    | `ci.yml:~160`     |
| `daemonunit-check (generated drift)`                        | `pkg/daemonunitspec/*.go` drift           | `ci.yml:~600`     |
| `sqlc-check (generated drift)`                              | sqlc query drift                          | `ci.yml:~440`     |
| `sdk-go build + test`                                       | sdk/go compilation + tests                | `ci.yml:~520`     |
| `sdk-node (gen-check + smoke + unit)`                       | sdk/node drift                            | `ci.yml:~540`     |
| `sdk-python (gen-check + smoke + unit)`                     | sdk/python drift                          | `ci.yml:~580`     |
| `proto-check`                                               | checked-in `*.pb.go` matches protoc       | `ci.yml:200-208`  |
| `load (1k rps hot-path)`                                    | p50 regression under load (issue #266)    | `ci.yml:~700`     |
| `workflow-lint (actionlint)`                                | Workflow YAML semantic lint               | `ci.yml:~1175`    |

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
locally, in this order (each one is its own atomic gate so a failure
points at exactly one cause):

1. `spec-check`          — `api/openapi.yaml` ↔ `pkg/apid/openapi.yaml`
                           drift + vacuum lint + AST parity.
2. `spec-meta-lint`      — `openapi-spec-validator==0.7.1` against the
                           3.1.0 meta-schema (catches the structural
                           errors vacuum silently accepts: bad `$ref`,
                           missing schema, duplicate operationId, etc.).
3. `spec-endpoint-drift` — current spec vs PR-base spec, cross-checked
                           against the three customer-facing SDKs (Node
                           generated services + Python generated api +
                           Go `pkg/api/client.go` AST). Fails on
                           removal/rename of an SDK-exposed (path, method).
4. `proto-check`         — checked-in `*.pb.go` matches protoc.
5. `sqlc-check`          — checked-in sqlc output matches regenerated.
6. `egress-check`        — nftables render + Go cross-check.
7. `images-lock-check`   — every `images/*.Dockerfile` FROM is digest-
                           pinned via `images/Dockerfile.lock`.
8. `images-hadolint-check` — Dockerfile best practices via hadolint.
9. `grafana-jq-check`    — dashboard JSON parses cleanly (`jq -e .`).
10. `grafana-mirror-check` — `deploy/grafana/` byte-identity mirror to
                             the ansible role.
11. `workflow-lint`       — `actionlint` over `.github/workflows/*.yml`.
12. `sdk-gen-node-twice`  — Node SDK determinism (regen twice, zero diff).
13. `sdk-gen-python-twice` — Python SDK determinism.
14. `sdk-gen`             — single-shot regenerate-and-diff as final
                            baseline (catches a missed step in the
                            per-SDK recipes above).

Does NOT cover CI-only jobs that need Postgres service containers
(`lint + build` / `unit tests (pg shard 1/2)` / `unit tests (pure Go
shard 1/2)` / `e2e`). Those still surface in CI only.
