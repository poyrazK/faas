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

## Two CI tiers

CI is split so the complete suite runs a few times a day instead of on every
push:

| Tier | Workflow | Runs on | Contents |
|---|---|---|---|
| Light | `ci-light.yml` | every pull request push, every merge to `main` | golangci-lint on changed packages, `go build ./...`, vet and `-race` tests for changed packages plus their direct importers (`scripts/ci/affected_packages.py`), generated-code drift, policy gates, migration ID hygiene plus apply/replay when migrations change |
| Mega | `ci.yml` | nightly on `main`, `workflow_dispatch`, and from `release.yml` | the complete suite: every unit shard, e2e, migration history, container/UDP contracts, flags, load, SDK acceptance, ops/infra gates |

`release.yml` calls `ci.yml` (`full-ci`) and every build/publish job depends
on it, so no release tag publishes unless the full suite passed on the tagged
commit. Transitive breakage beyond one import level that the light tier
misses is caught by the nightly run or, at the latest, the release gate.

## Currently required

Regenerate this table from the live ruleset rather than editing it by hand:

```
gh api repos/poyrazK/faas/rulesets/19061133 \
  --jq '.rules[]|select(.type=="required_status_checks")|.parameters.required_status_checks[].context'
```

| Job name (exact) | Protects | Where |
|---|---|---|
| `light: lint, vet, build` | golangci-lint on changed packages (same rules and per-package invocation as the mega `Go lint` shards), gofmt, vet, repo-wide build, PR-4a fix-has-test | `ci-light.yml:lint` |
| `light: codegen, policy, migrations` | sqlc/proto/spec/SDK/daemonunit drift, repo policy gates, migration ID hygiene, apply-and-walk and replay safety | `ci-light.yml:checks` |
| `light: tests (shard 1)` | `-race` tests of affected packages | `ci-light.yml:tests` |
| `light: tests (shard 2)` | `-race` tests of affected packages | `ci-light.yml:tests` |
| `light: tests (shard 3)` | `-race` tests of affected packages | `ci-light.yml:tests` |
| `runtime-contract-gate` | Runtime image, source-artifact, adapter, and operator-doc contracts | `images.yml` |

A light test shard with no affected packages succeeds immediately, so a
docs-only change still reports every required context.

## Not required (deliberately or not yet)

Every `ci.yml` job (`Go lint` shards, `lint + build`, the unit, state, e2e and
migration-history shards, `checks (drift + contract gates)`, load, SDK and
acceptance jobs) no longer runs on pull requests. They gate releases through
`release.yml` instead and report nightly on `main`; the previous required
contexts (`lint + build`, `unit tests (…)`, `e2e (shard N — …)`,
`migrations (IDs + apply)`, `load (1k rps hot-path)`, `sdk-node (…)`,
`sdk-python (…)`, `boot-contract (production config)`,
`checks (drift + contract gates)`) must be removed from the ruleset.

`CodeQL` and the path-filtered acceptance workflows (`Data API acceptance`,
`MCP contract`, `Customer platform starter`) run on pull requests but do not
block a merge.

The metal gates (`e2e-native`, `builder-native`) are deliberately excluded:
they run on dedicated hardware, are dispatch-only, and cannot gate a PR.

### Switching the ruleset to the light tier

The pull request that introduces `ci-light.yml` cannot report the old
`ci.yml` contexts, so the switch is one coordinated ruleset edit:

1. replace the old `ci.yml` contexts in ruleset `19061133` with the five
   `light: …` contexts above (keep `runtime-contract-gate`);
2. merge the pull request, whose own run reports exactly those contexts.

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
