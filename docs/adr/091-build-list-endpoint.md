# ADR-091 · First-class build list endpoint (issue #741 close-out / DEPLOY-PROV-6 follow-up)

- **Status:** accepted
- **Date:** 2026-08-10
- **Issue:** #741 (third acceptance criterion — operator "what's building now?" view)
- **Supersedes:** the implicit "log into Postgres to enumerate builds" path (`select id, status, started_at, finished_at from builds where status='running'`), the implicit "log into Postgres per app" CI script path that calls `BuildByDeployment` in a loop.
- **Related:** ADR-089 (the sibling single-id endpoint — same scope gate, same `BuildResponse` shape, same `b.status` lifecycle semantics). ADR-038 (origin of the `builds` table). ADR-034 (scope rules — read surface uses `ScopesReadSurface`). spec §14 build lifecycle. `pkg/state/pgstore.go:11416-11446` (the existing unlimited `ListBuildsForAccount` — used by the GDPR export at `cmd/apid/handlers_account.go:643`, stays intact as a sibling). `cmd/gregale/commands_deployments.go:65-134` (canonical list-shape CLI).

## Context

PR #792 (DEPLOY-PROV-6 / ADR-089) shipped `GET /v1/builds/{id}` — the
single-id build-status endpoint. ADR-089 §3 listed the list surface
under deferred items and explicitly scoped the work to the single
id. Issue #741's third acceptance criterion — operators answering
"what's building now?" — was not closed by that PR.

Today, the only path to enumerate builds is
`ListBuildsForAccount(ctx, accountID)` in `pkg/state/pgstore.go:11416`,
used by the GDPR export at `cmd/apid/handlers_account.go:643`.
That method is **uncursor'd, unlimited, and unfilterable** — it
returns every build the account owns in one query and does not
accept a status filter, app filter, or pagination. Operators
log into Postgres directly; CI scripts that want "still running
for app X" must call `BuildByDeployment` per deployment in a loop.

The new endpoint mirrors `GET /v1/deployments` exactly. The
deployments list handler (`cmd/apid/handlers_ext.go:3068-3114`)
and its underlying keyset SQL (`pkg/state/pgstore.go:4426-4459`)
are the canonical reference shape.

## §3 Sub-decisions

1. **Cursor pagination, not offset.** Keyset on
   `started_at desc nulls last` mirrors `ListDeploymentsForAccount`.
   The `builds` table grows unbounded per app — Pro plan allows 50
   deploys per app; offset on a large table is a foot-gun. The
   keyset filter (`started_at < $before`) is the same shape the
   deployments list uses; customers can pass the response's
   `next_before` straight back as `?before=<cursor>`.

2. **Account-scoped by default; `?app=<slug>` narrows.** Mirrors
   `GET /v1/deployments`. The SQL itself filters on
   `a.account_id = $1` so cross-account data never leaves the
   store. No operator-only "every account" surface in this PR —
   operators use the customer's surface scoped to their own
   account.

3. **4-value status enum.** `queued|running|succeeded|failed`.
   No `?status=all` magic — omit the param for "any status".
   Bad values render `400 CodeValidation`. The enum matches the
   `builds.status` CHECK constraint (schema.sql:662) and the
   single-id endpoint's `BuildResponse.Status` semantics.

4. **App-scope IDOR = `AppBySlug` + `App.AccountID == acct.ID`.**
   Cross-account slug renders `404 app_not_found` (existing
   `CodeAppNotFound`, same envelope as `getApp`). The check
   happens BEFORE the SQL runs so cross-account slug probes
   never enumerate row counts.

5. **Envelope = `{items, next_before}`.** Matches
   `DeploymentListResponse` (dto.go:1594-1604). Backward-only
   cursor. `NextBefore` uses the LAST row with a **non-null**
   `started_at` so passing the cursor never skips the
   running/succeeded rows behind queued builds at the tail of
   the previous page. (If the page is full of queued builds the
   cursor is empty — there are no further rows to page to.)

6. **No new SDK error code.** Filter errors use existing
   `CodeValidation` (bad status, bad cursor, bad limit).
   Unknown-app uses existing `CodeAppNotFound`. Bad cursor
   surfaces as `400 Bad cursor / expected RFC3339 timestamp`,
   identical to `listDeployments`.

7. **SDK method = `GetBuilds`** (NOT `ListBuilds`). The
   auto-derivation at `cmd/sdk-coverage/main.go:515-527`
   produces `GetBuilds` from `GET /v1/builds` already, and the
   existing aggregate convention uses `Get<Resource>` for
   account-scoped aggregates that replace N per-app calls
   (`/v1/instances` → `GetInstances`,
   `/v1/secrets` → `GetSecrets`,
   `/v1/apps/metrics` → `GetAppsMetrics` — explicit comment at
   `cmd/sdk-coverage/main.go:306-310`). No `methodRouteMap`
   entry needed; the drift gate at
   `cmd/sdk-coverage/main.go:483-489` requires only that the
   resolved name exist on `*Client`. The Node + Python SDK
   generators pick up `GetBuilds` automatically.

8. **Schema change: `builds(deployment_id, started_at desc nulls last)`.**
   New index supports both the keyset filter AND the existing
   `BuildByDeployment` single-id lookup (`queries.sql:353`).
   Forward-only addition in `Up`; `DROP INDEX` in `Down`. The
   migration is `migrations/00174_builds_deployment_started_idx.sql`.
   Rationale for the column order: the planner's most likely
   strategy is nested-loop through `apps` (via
   `apps_account_idx`) → `deployments` (via
   `deployments_app_idx`) → `builds` via
   `builds_deployment_started_idx`. The composite lets the
   inner step satisfy BOTH the join probe AND the
   `started_at < $before` filter + `ORDER BY started_at desc
   nulls last` from the index alone.

9. **State layer = sibling paged method.** Keep the existing
   `ListBuildsForAccount(ctx, accountID)` intact for the GDPR
   export (the GDPR export wants every row in one slice). Add
   `ListBuildsForAccountPaged(ctx, accountID, statusFilter,
   appIDFilter string, before time.Time, limit int)` for the
   new endpoint. Two methods, two purposes — neither breaks
   the other.

10. **`requireMFA` deliberately omitted.** Mirrors
    `GET /v1/builds/{id}` per ADR-089 §6. The builds route
    family uses `authLimited(requireScope(ScopesReadSurface...))`
    without `requireMFA`; this is a documented divergence from
    `GET /v1/deployments` (which has `requireMFA`). The route
    mount comment + handler header both call this out so a
    reviewer doesn't flag it as a copy-paste bug.

11. **CLI = `gregale build list`** mirroring `gregale deployments`.
    Flags: `--app SLUG`, `--status S` (4-value enum),
    `--limit N` (1-200, default 50), `--before CURSOR`
    (RFC3339Nano), `--all` (walks every page). The cursor
    hint (`... more — pass --before CURSOR`) uses an em-dash
    (U+2014) byte-for-byte identical to
    `cmd/gregale/commands_deployments.go:109`. Test pins this.

12. **Rollback:** revert the 5 commits in reverse. `DROP INDEX`
    in migration `Down`. No other DB changes. GDPR export path
    unaffected (the new sibling method is purely additive). No
    customer-facing API breaks.

## Implementation surface

| Layer | File | Change |
|-------|------|--------|
| ADR | `docs/adr/091-build-list-endpoint.md` | This file. |
| Migration | `migrations/00174_builds_deployment_started_idx.sql` | NEW. Index. |
| Migration test | `migrations/00174_builds_deployment_started_idx_test.go` | NEW. Apply-walk pins it. |

> **Renumber note (PR #803):** this migration was originally
> written as `00166` in the pre-review draft. During the post-
> review CI investigation, the sibling fence
> `00166_reserve_slot.sql` from another PR appeared on
> `origin/main` (alongside `00167_apps_overflow_node.sql`),
> colliding with the slot. Per the cross-PR fence pattern
> (memory: `cross-pr-slot-gate-reservation-fence-pattern.md`),
> the migration was renumbered to `00168` (the next free slot
> after 167). A re-test of the renumber against
> `origin/main` showed 168 was also fenced (and 169, 170, 171,
> 172 fences + 173 real), so a second renumber landed at
> `00174` — the next free slot after 173. The test corpus +
> this ADR were updated. The index name
> `builds_deployment_started_idx` is unchanged, so no code
> beyond the file rename was touched.
| State interface | `pkg/state/store.go` | ADD `ListBuildsForAccountPaged`. |
| State impl | `pkg/state/pgstore.go` | ADD keyset SQL impl. |
| State impl | `pkg/state/memstore.go` | ADD slice-filter impl. |
| State coverage | `pkg/state/pgstore_coverage_sweep_test.go` | ADD rows. |
| State coverage | `pkg/state/memstore_coverage_slice*_test.go` | ADD mirror. |
| DTO | `pkg/api/dto.go` | ADD `BuildListResponse`. |
| SDK | `pkg/api/client.go` | ADD `GetBuilds`. |
| SDK sweep | `pkg/api/client_method_sweep_test.go` | ADD `TestSweep_GetBuilds`. |
| OpenAPI | `api/openapi.yaml` | ADD `/v1/builds` path + `BuildListResponse` schema. |
| OpenAPI embed | `pkg/apid/openapi.yaml` | regenerated via `make spec-sync`. |
| Route | `cmd/apid/server.go` | ADD `GET /v1/builds` mount. |
| Handler | `cmd/apid/handlers_ext.go` | ADD `listBuilds`. |
| Handler tests | `cmd/apid/handlers_build_list_test.go` | NEW. 13 subtests. |
| CLI | `cmd/gregale/commands_builds.go` | ADD `cmdBuildList` + `renderBuildListRow` + dispatch case. |
| CLI tests | `cmd/gregale/commands_builds_test.go` | ADD (em-dash byte-for-byte). |
| SDK node | `sdk/node/src/generated/services/DeploymentsService.ts` | regenerated. |
| SDK python | `sdk/python/faas_sdk/api/deployments/get_builds.py` + model | regenerated. |

## Verification

### Local

- `make sqlc-check` — green (no sqlc change; new method is inline SQL).
- `make spec-check` — green (new DTO + route + schema detected by
  `cmd/apid/spec_compliance_test.go::TestSpecCompliance`).
- `make lint` — green (`gofmt -l` repo-wide; new files gofmt-clean).
- `go test ./pkg/api/... ./pkg/state/... ./cmd/apid/... ./cmd/gregale/...` — green.

### CI

- `lint + build` — green.
- `unit tests (pure Go shard 1/2 + pg shard 1/2)` — green. New
  whitebox handler tests + SDK sweep test land in pg shard 2.
- `spec-check` — green.
- `sdk-go` (gated by `make sdk-check`) — green. No
  `methodRouteMap` entry; auto-derivation produces `GetBuilds`.
- `sdk-node (gen-check + smoke + unit)` — green.
  Regenerated `DeploymentsService.getBuilds` +
  `BuildListResponse` model.
- `sdk-python (gen-check + smoke + unit)` — green.
  Regenerated `get_builds.py` + model.
- `daemonunit-check (generated drift)` — green. No daemon unit
  change.
- `migrations (contiguity + apply)` — green. The new
  `00174_builds_deployment_started_idx.sql` runs cleanly forward
  + back. Apply-walk test pins it.
- `e2e (4 shards)` — green. Blackbox e2e untouched.

### Acceptance gate (closes issue #741)

```
$ go test ./pkg/api/... -run TestSweep_GetBuilds -v
--- PASS: TestSweep_GetBuilds

$ go test ./cmd/apid/... -run TestListBuilds -v
=== RUN   TestListBuilds_OK_AccountWide
=== RUN   TestListBuilds_OK_AppFilter
=== RUN   TestListBuilds_OK_StatusFilter
=== RUN   TestListBuilds_OK_Pagination
=== RUN   TestListBuilds_OK_Empty
=== RUN   TestListBuilds_OK_NullsLast
=== RUN   TestListBuilds_AppIDOR_OtherAccount
=== RUN   TestListBuilds_AppIDOR_Missing
=== RUN   TestListBuilds_BadStatusFilter
=== RUN   TestListBuilds_BadCursor
=== RUN   TestListBuilds_BadLimit
=== RUN   TestListBuilds_RequiresAuth
=== RUN   TestListBuilds_RateLimit
--- PASS: TestListBuilds (13 subtests passed)

$ go test ./cmd/gregale/... -run TestCmdBuildList -v
... all subtests PASS

$ make spec-check
ok  	github.com/onebox-faas/faas/cmd/apid

$ make sdk-check
ok  	github.com/onebox-faas/faas/cmd/sdk-coverage

$ make test
... all green
```

### Rollback

Revert the 5 commits in reverse. `DROP INDEX` in migration
`Down`. GDPR export path unaffected (the new sibling method is
purely additive). No customer-facing API breaks.

Issue #741 closes when this PR lands alongside PR #792 — ADR-089
covers the single-id endpoint, ADR-091 covers the list surface.
