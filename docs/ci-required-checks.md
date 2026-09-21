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

Regenerate this table from the live ruleset rather than editing it by hand:

```
gh api repos/poyrazK/faas/rulesets/19061133 \
  --jq '.rules[]|select(.type=="required_status_checks")|.parameters.required_status_checks[].context'
```

| Job name (exact) | Protects | Where in ci.yml |
|---|---|---|
| `boot-contract (production config)` | Daemons boot from production-rendered config with production capabilities (issue #1529 / ADR-075) | `ci.yml:boot-contract` |
| `checks (drift + contract gates)` | Generated-artifact drift plus the repo policy gates (env contract, spec-cited tests, migrations hygiene) | `ci.yml:checks` |
| `e2e (shard 1 — 18 tests)` | Sharded `./cmd/e2e` — real daemons against real Postgres | `ci.yml:e2e-shard1` |
| `e2e (shard 2 — 15 tests)` | Sharded `./cmd/e2e` | `ci.yml:e2e-shard2` |
| `e2e (shard 3 — 24 tests)` | Sharded `./cmd/e2e` | `ci.yml:e2e-shard3` |
| `e2e (shard 4 — 22 tests)` | Sharded `./cmd/e2e` | `ci.yml:e2e-shard4` |
| `lint + build` | golangci-lint + gofmt + vet + build, repo-wide | `ci.yml:test` |
| `load (1k rps hot-path)` | p50 regression under load (issue #266) | `ci.yml:load` |
| `migrations (IDs + apply)` | Migration ID, replay, and ledger-set safety (ADR-142) | `ci.yml:migrations` |
| `runtime-contract-gate` | Runtime image, source-artifact, adapter, and operator-doc contracts | `images.yml` |
| `sdk-node (gen-check + smoke + unit)` | sdk/node drift | `ci.yml:sdk-node` |
| `sdk-python (gen-check + smoke + unit)` | sdk/python drift | `ci.yml:sdk-python` |
| `unit tests (pg shard 1 — apid/meter/migrations)` | apid + meter + migrations + db + alerts | `ci.yml:unit-tests-pg-1` |
| `unit tests (pg shard 2a — state/reconcile/reposcan)` | pkg/state + reconcile + reposcan | `ci.yml:unit-tests-pg-2` |
| `unit tests (pg shard 2b — gregale/gregalectl/daemons)` | gregale + gregalectl + meterd + schedd | `ci.yml:unit-tests-pg-2` |
| `unit tests (pure Go shard 1 — sched/fcvm/gateway)` | sched + fcvm + gateway (-race) | `ci.yml:unit-tests-pure-1` |
| `unit tests (pure Go shard 2 — light packages)` | the rest of the race-enabled tree | `ci.yml:unit-tests-pure-2` |

## Not required (deliberately or not yet)

`spec-check (OpenAPI lint + AST parity)`, `CodeQL`, `supply-chain-scan
(govulncheck high+)`, `proto-check`, `daemonunit-check`, `sqlc-check`, `sdk-go
build + test`, `terraform provider build and test`, and `workflow-lint
(actionlint)` run on every PR but do not block a merge. Several are folded into
`checks (drift + contract gates)`, which is required; the standalone ones are
not.

The metal gates (`e2e-native`, `builder-native`) are deliberately excluded:
they run on dedicated hardware, are dispatch-only, and cannot gate a PR.

## Renaming a required job

GitHub matches a status check by its exact `name:` string, so renaming a
required job in `ci.yml` makes its context stop reporting — and a required
context that never reports blocks every PR, including the one doing the rename.

The rename therefore cannot be done in a single PR. The sequence is:

1. remove the old context from ruleset `19061133`;
2. merge the PR that renames the job;
3. add the new context to the ruleset.

Step 1 leaves the branch briefly unprotected by that check, which is why the
e2e shard names still carry stale test counts (`18/15/24/22` against roughly
370 actual tests). Correcting them is a coordinated ruleset edit, not a code
change, and is worth doing only alongside another required-check change.
