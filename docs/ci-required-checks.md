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
| Light | `ci-light.yml` | every pull request push, every merge to `main` | about 10 minutes per job: golangci-lint (govet included) and `-race` tests for changed packages (`scripts/ci/affected_packages.py`), the §6.2 invariant property tests when anything in their import tree changes, for `cmd/gregale`, `pkg/sched`, `pkg/state` and `pkg/apid/apidserver` only the tests related to the change, capped and without `-race` (`scripts/ci/select_package_tests.py`), boot-contract, generated-code drift, policy gates, version-skew checks against the previous release and the migration expand/contract lint ([release-compatibility.md](release-compatibility.md)), migration ID hygiene plus apply/replay when migrations change |
| Mega | `ci.yml` | nightly on `main`, `workflow_dispatch`, and from `release.yml` | the complete suite: every unit shard, e2e, migration history, container/UDP contracts, flags, load, SDK acceptance, ops/infra gates |

`release.yml` calls `ci.yml` (`full-ci`) and every build/publish job depends
on it, so no release tag publishes unless the full suite passed on the tagged
commit. What the light tier deliberately skips (importers' compilation and tests, the
full `cmd/gregale`/`pkg/state`/`pkg/apid/apidserver` suites and their `-race` runs, e2e,
migration history, container/UDP contracts, flags, load, SDK acceptance) is
caught by the nightly run or, at the latest, the release gate.

`blast-radius.yml` runs on every pull request and is report-only (never
required): `scripts/ci/blast_radius.py` lists the release binaries whose
import closure the change touches (what a per-unit release ships,
ADR-1650) and, of those, the ones whose code changes, built at the merge
base and the head with the release flags and an empty Go build ID; a
binary whose only difference is Go's line table (`.gopclntab`) is listed
as `lines`. The report is the job summary and one sticky pull-request
comment. Run it locally with
`python3 scripts/ci/blast_radius.py --base origin/main` (`--closure-only`
skips the builds; `--tags` measures a build-tag change to the recipe).

## Currently required

Regenerate this table from the live ruleset rather than editing it by hand:

```
gh api repos/poyrazK/faas/rulesets/19061133 \
  --jq '.rules[]|select(.type=="required_status_checks")|.parameters.required_status_checks[].context'
```

| Job name (exact) | Protects | Where |
|---|---|---|
| `light: lint, vet, build` | golangci-lint (govet included) on changed packages, `go vet` of the unchanged importers that use a changed exported name, gofmt, PR-4a fix-has-test | `ci-light.yml:lint` |
| `light: codegen, policy, migrations` | sqlc/proto/spec/SDK/daemonunit drift, repo policy gates, version skew (protos and guest protocol vs the previous release), migration expand/contract lint, migration ID hygiene, apply-and-walk and replay safety | `ci-light.yml:checks` |
| `light: tests (shard 1)` | `-race` tests of changed packages and the §6.2 invariants; when `cmd/gregale` changed, its tests related to the change (no `-race`) | `ci-light.yml:tests` |
| `light: tests (shard 2)` | `-race` tests of changed packages and the §6.2 invariants; when `pkg/state` changed, its tests related to the change (no `-race`) | `ci-light.yml:tests` |
| `light: tests (shard 3)` | `-race` tests of changed packages and the §6.2 invariants; when `pkg/apid/apidserver` changed, its tests related to the change (no `-race`) | `ci-light.yml:tests` |
| `boot-contract (production config)` | Daemons boot from production-rendered config with production capabilities (issue #1529 / ADR-075) | `ci-light.yml:boot-contract` (same name in `ci.yml`) |

A light test shard with no affected packages succeeds immediately, so a
docs-only change still reports every required context.

## Not required (deliberately or not yet)

`runtime-contract-gate` (`images.yml`: runtime image, source-artifact,
adapter and operator-doc contracts) stopped being required on 2026-10-17 so
pull requests do not wait for the images workflow; it still runs and reports.

Every `ci.yml` job (`Go lint` shards, `lint + build`, the unit, state, e2e and
migration-history shards, `checks (drift + contract gates)`, load, SDK and
acceptance jobs) no longer runs on pull requests. They gate releases through
`release.yml` instead and report nightly on `main`; the previous required
contexts (`lint + build`, `unit tests (…)`, `e2e (shard N — …)`,
`migrations (IDs + apply)`, `load (1k rps hot-path)`, `sdk-node (…)`,
`sdk-python (…)`, `checks (drift + contract gates)`) must be removed from
the ruleset. `boot-contract (production config)` stays: the light tier
reports it under the same name.

`CodeQL` no longer runs on pull requests: it scans every push to `main`, a
weekly schedule, and any branch on demand (`workflow_dispatch`). The
acceptance workflows (`Data API acceptance`, `MCP contract`, `Customer
platform starter`, `durable-entity-developer-workflow`, `S3 multipart
integrity`) run on a pull request only when their own code changes, never
for shared paths such as `pkg/api`, `pkg/state`, `pkg/apid`, `migrations` or
`go.mod`; `ci.yml` calls all five, so they run nightly and gate every
release. `images.yml` builds `builder-base` for linux/amd64 only on pull
requests (main pushes build and scan both architectures) and decides from
the pull request's merge base whether an image input changed. None of these
block a merge.

`cancel conflicted PR runs` sweeps on every push to `main` and every 15
minutes: it cancels the queued and running pull request runs of PRs that now
conflict with their base (`scripts/ci/cancel_conflicted_pr_runs.py`). GitHub
already skips starting runs for a conflicting PR; this catches runs that
started while the PR was clean.

The metal gates (`e2e-native`, `builder-native`) are deliberately excluded:
they run on dedicated hardware, are dispatch-only, and cannot gate a PR.

### Switching the ruleset to the light tier

The pull request that introduces `ci-light.yml` cannot report the old
`ci.yml` contexts, so the switch is one coordinated ruleset edit:

1. replace the old `ci.yml` contexts in ruleset `19061133` with the five
   `light: …` contexts above (keep `boot-contract (production config)`);
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
