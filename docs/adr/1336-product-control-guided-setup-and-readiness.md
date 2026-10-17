# ADR-1336 · Product Control guided setup and observed readiness

- **Status:** accepted for internal implementation
- **Date:** 2026-10-15
- **Decision:** Add reviewed catalog setup to the internal owner console, reuse
  Operations diagnostics for product readiness, and provide repeatable read-only
  pilot evidence verification. Keep the existing default-off flag and separate
  native qualification requirements.

## Guided publication

The setup form connects an existing account-owned Application Auth app, one named
Operation in an explicit environment, and two named plan grants. It creates or
updates these entries while preserving other features, plans and grants. The
current catalog currency is fixed. Prices and start allowances use the existing
integer limits. Existing tenants are selected from bounded pages by name; their
assignments continue through the existing reviewed plan-change flow. First
assignments may link existing Auth organization UUIDs. User invitations and
organization creation stay in Application Auth; no admin key is collected.

A review compares current and proposed prices/caps and renders the complete
resulting catalog. Its canonical payload and expected head are bound to the
owner/app CSRF confirmation. Publication validates the owned Auth binding again
and checks the expected head under the account → shared app lock order. An
updated current head conflicts before writes. A retry whose desired revision
is still current returns that revision. A delayed retry after a different head
was published requires a new review and never restores the old head.

Publishing does not migrate customers or replace identity links. Assigned price
revisions remain immutable. The latest Auth selector still controls the portal
realm for all customers, and its access impact is explicitly reviewed. Existing
API/CLI catalog publication retains its original replacement behavior.

## Readiness observations

The private owner `GET /v1/apps/{slug}/product/doctor` and dashboard readiness view
observe the responding API node. Selecting a tenant additionally checks its
ownership, assignment, suspension, organization-link configuration, assigned
feature grants and current-month allowance. Binding inspection and entitlement
remain distinct. An unscoped report explicitly leaves customer prerequisites
unverified. Assigned and latest catalog revisions are identified separately.

Resolved definitions reuse the existing Operations doctor for plan limits,
deployment/release metadata, pending capacity, preview cohort and execution-kind
policy, workload-trust configuration, artifact-storage configuration, and optional
completion destinations. An unavailable or mismatched observation fails closed
as unverified. These reads reserve no quota, submit no work and enable no gate.

Configuration states are `observed`, `blocked` or `unverified`. Configured trust,
storage or Auth bindings do not prove live usability. Live Auth login/membership,
storage I/O, gateway behavior, native lifecycle and fleet rollback remain separate
qualification checks. This doctor always reports native qualification unverified.
The CLI validates response scope and aggregate state before printing a result.

## Repeatable pilot evidence

`product pilot init` creates a private, non-overwriting manifest template.
`product pilot verify` requires exactly three distinct baseline customers on two
plans, real unique Operation IDs, immutable revisions, explicit outcomes and
integer expected charges. Additional cases belong to those same customers.

Verification reads scoped explanations, compiles pinned catalogs to recheck their
hashes, compares admission/quote/allowance evidence, first verified terminal
outcomes or retained reconciliation states, success prices, permanent waivers,
and admission-month billing lines. Aggregate counts and amounts must cover all
referenced receipts together; one monthly success cannot cover several cases.
The selected cases never establish a complete monthly total. Reads are an
observation window; concurrent changes can require a repeat.

Optional local CSV files are bounded, relative to the manifest directory, opened
through `os.Root` and SHA-256 checked without including their contents in reports.
These hashes are local-file evidence, not proof of customer download or exporter
correctness. The JSON report records the canonical manifest digest, receipts,
reviews, node doctor and output hashes. Report files use mode 0600 and exclusive
creation. The verifier performs no start, retry, cancellation, deployment,
assignment, flag change, period closure or external invoice request.

Ledger observations may match while qualification remains unverified. Customer
login/isolation, browser retry recovery, actual private downloads, redeployment,
plan-change behavior, native interruption/reconciliation and flag rollback need
the documented live trial and retained source-qualified evidence. No supplied
manifest field can mark those scenarios qualified. Real month closure waits for
the original UTC admission month to close; clocks and receipts are never changed.

## Bounds and validation

Setup forms are 16 KiB; signed catalog confirmations are bounded at 128 KiB and
catalogs at 64 KiB. Customer/app suggestions use 100-entry bounds. Doctor reports
have at most 1,024 checks and a 20-second observation deadline. Pilot manifests
are 64 KiB with at most 32 cases; relative output paths are at most 512 bytes and
each local CSV is at most 16 MiB. Bounds live in `pkg/api/limits.go`.

Memory/PostgreSQL contracts cover publication races, lost-response retries,
subsequent-head protection, currency invariance and preserved assignments. HTTP
tests cover default-off setup/doctor routes, CSRF and payload tampering, stale
reviews, customer scope, preserved entries and revision-specific diagnostics.
Domain/CLI tests distinguish configured from qualified, validate manifests and
response scopes, preserve evidence files, and reject output-path escapes.
