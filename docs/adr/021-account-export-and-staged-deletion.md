# ADR-021 · Account export and staged deletion (G6)

- **Status:** proposed
- **Date:** 2026-07-18
- **Decision:** Ship a customer-facing GDPR self-service surface (export
  bundle + 30-day staged deletion + restore) on top of apid, with a
  one-shot hard delete driven by a 30 s grace timer inside the apid
  process.
- **Why:** §17 gap G6 is the only spec-§17 row still open and an
  explicit M8 row-level blocker. The design must be auditable,
  customer-facing, and end-to-end testable from a single CLI round
  trip — not a support-ticket workflow.
- **Consequences:** Closes G6; unblocks M8 sign-off; introduces a
  grace-timer goroutine in apid (no new daemon); widens the
  customer-intent surface with three new endpoints plus a public
  DPA template.
- **Rejected alternatives:** (1) ON DELETE CASCADE migration —
  schema-shape change for one feature; deferred. (2) Hard delete on
  DELETE /v1/account — too destructive; lost data is unrecoverable
  in a one-box backup model. (3) Storing the DPA in the database —
  plaintext template lives in the repo so it can be PR-reviewed.

## Decisions

- **D1 export shape** — single JSON document with slices per
  resource type (apps, deployments, builds, instances, usage,
  domains, crons, API keys, app secrets). One file is easier to
  diff, review, and gzip on the wire than a tar.gz bundle.
- **D2 grace state** — existing `AccountDeletedPending` status plus
  new `accounts.deletion_requested_at timestamptz` column. No new
  enum value, no migration to the status CHECK.
- **D3 restore** — `POST /v1/account/restore` flips back to `active`
  iff `now < deletion_requested_at + 30 d`. Past the deadline the
  handler returns `409 account_not_restorable` (the only honest
  answer — the row is gone or about to be).
- **D4 secrets in export** — ciphertext passthrough (base64). Default
  ON. `?include_secrets=false` drops the slice entirely. Plaintext
  never lands in PG (ADR-020); the customer can rotate their host
  age key after a restore-from-export without losing the per-secret
  envelope.
- **D5 DPA** — `docs/DPA.md` plaintext template served at
  `GET /v1/account/dpa` with no auth. Public artefact a prospect
  reads before signing up; markdown so it renders in the dashboard
  help panel without a second HTML pass.
- **D6 timer location** — `pkg/grace` inside apid. The grace write
  side is apid; meterd is for quotas/billing only. Keeping the
  timer next to the write path avoids a pg_notify loop just to
  schedule a deletion.
- **D7 auth during grace** — `s.auth` is loosened for
  `/v1/account*` paths during `deleted_pending` so the customer can
  grab a final export or cancel via restore. All other routes still
  402 with `CodeBillingPastDue` because the work surface (deploy,
  build, park live instances) is already torn down at this point.
- **D8 dashboard DELETE** — `/dashboard/account/delete` POST
  proxies to `s.scheduleDeletion` directly inside the same process
  (the dashboard form path doesn't need a CSRF-protected HTTP
  round trip; the sessionAuth middleware already anchors the call
  to the logged-in account). The form requires a `confirm_token`
  hidden field equal to `"delete:yes"`; the matching restore token
  is `"restore:yes"`. These tokens are depth-of-defence against
  CSRF on top of the same-origin cookie requirement.

## Files

### New

| Path | Purpose |
|---|---|
| `migrations/00010_account_deletion.sql` | Adds `accounts.deletion_requested_at` + partial index |
| `pkg/grace/grace.go` | 30-day grace timer (apid-local) |
| `pkg/grace/grace_test.go` | MemStore-driven unit tests for RunOnce |
| `cmd/apid/handlers_account.go` | `exportAccount`, `deleteAccount`, `restoreAccount`, `dpaTemplate`, `gatherExport`, `writeDeletionEnvelope`, `scheduleDeletion`, `cancelDeletion` (each ≤50 LoC) |
| `cmd/apid/handlers_account_test.go` | MemStore handler tests |
| `cmd/apid/dashboard_delete.go` | `/dashboard/account/delete` + `/restore` POSTs |
| `pkg/mail/account.go` | `AccountDeletionPendingBody` + `AccountDeletionCompleteBody` |
| `cmd/faas/commands4.go` | `faas account {export,delete,restore,status}` |
| `cmd/faas/commands4_test.go` | CLI dispatch + flag tests |
| `docs/DPA.md` | Plaintext DPA template with `{{…}}` placeholders |
| `docs/adr/021-account-export-and-staged-deletion.md` | This ADR |
| `pkg/state/pgstore_account_deletion_test.go` | pgtest round-trips |
| `cmd/e2e/account_e2e_test.go` | End-to-end tests against real Postgres |

### Modified

| Path | Change |
|---|---|
| `pkg/state/store.go` | 6 new methods on the `Store` interface |
| `pkg/state/pgstore.go` | Implementations; DeleteAccount uses BeginTx |
| `pkg/state/memstore.go` | Mirror implementations |
| `pkg/state/types.go` | `Account.DeletionRequestedAt *time.Time` |
| `pkg/api/dto.go` | `AccountExportResponse` + 5 sub-DTOs + `AccountDeletionResponse` |
| `pkg/api/errors.go` | 3 new error codes (`account_deletion_*`, `account_not_restorable`) — all 409 |
| `pkg/db/notify.go` | `NotifyAccountDeletionPending`, `NotifyAccountDeleted` |
| `pkg/dashboard/dashboard.go` | `AccountData` gains danger-zone fields |
| `pkg/dashboard/templates/account.html` | Danger-zone partial with CSRF-protected forms |
| `cmd/apid/server.go` | 4 routes, auth carve-out via `isAccountScopedPath`, `dpaPath` field |
| `cmd/apid/main.go` | Wire `pkg/grace` into `deps.bgBefore` |
| `cmd/apid/handlers_dashboard.go` | Populate danger-zone fields on `AccountData` |
| `cmd/faas/main.go` | `case "account":` dispatch + usage line |
| `cmd/faas/client.go` | `ExportAccount`, `DeleteAccount`, `RestoreAccount` HTTP methods |
| `docs/adr/README.md` | New row in log table |

## Future work

- **ON DELETE CASCADE migration** — convert the per-table deletes in
  `PgStore.DeleteAccount` into schema-level cascades; ~1 KB SQL + a
  review of every dependent FK. Deferred until we have evidence the
  explicit walk is dropping a row in production.
- **`pkg/grace` generalization** — extract a `Timer` interface so
  future staged-action features (dunning ladder timers, free-tier
  hard stops) reuse the same Run/RunOnce pattern. Out of scope for
  G6.
- **PDF DPA generator** — a tiny `pdfgen` step that wraps
  `docs/DPA.md` into a signed PDF for prospects who insist on a
  binary file. Not blocking G6 — the markdown template is
  equally binding.
- **HMAC-based CSRF token** — replace the literal `"<action>:yes"`
  token with a per-session HMAC nonce; the helper signature stays
  the same so the dashboard template doesn't change.

## Rejected alternatives

- **CLI-only DELETE** — a customer who can't run the CLI (browser
  user, customer-success-assisted offboarding) has no recourse.
  Dashboard form is the only universally-available path.
- **Hard delete on DELETE** — destructive without recourse.
  Lost data is unrecoverable in the one-box backup model.
- **Meterd-owned grace timer** — meterd owns quotas + billing;
  coupling the deletion timer to the billing loop creates a
  shared failure domain where a billing-side incident blocks
  customer self-service.
- **State-store transaction wrapper** — `pkg/state.Store.Tx(...)`
  for arbitrary SQL: too generic for one feature. PgStore already
  uses pgx directly via `BeginTx` for the same reason.
- **Plaintext account-secret passthrough** — defeated the point of
  ADR-020; export's threat model includes "operator copies the
  bundle to a backup they don't control".

## Acceptance

- `make build && make test` green.
- `make test-load` (where applicable) green.
- `cmd/e2e/account_e2e_test.go` green against a real Postgres.
- Manual 30-day clock-advance path: set
  `deletion_requested_at = now() - interval '31 days'`, wait 60s
  for the grace tick, GET /v1/account/export → 401.
- All five `TestPg_*` round-trips green.
- All eight `handlers_account_test` subtests green.
- ADR-021 row added to `docs/adr/README.md`.