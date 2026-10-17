# ADR-1546 · Product Control customer readiness and activation overview

- **Status:** accepted for internal implementation
- **Date:** 2026-10-16
- **Decision:** Combine existing readiness, guided activation and retained work
  evidence into an owner-only, read-only customer overview. Reuse the default-off
  Product Control flag and existing reviewed remediation flows.

## Observation contract

`GET /v1/apps/{slug}/product/customers/overview`, the owner console's **Customer
readiness and activation** page, and `gregale product customers overview` share
one report. It lists assigned customers in ascending tenant UUID order, in pages
of at most ten and at most 3 MiB of JSON, below the CLI's response bound. Large
diagnostics produce smaller pages with the last emitted UUID as continuation;
they never drop the next customer. An optional `cursor` is the previous page's last canonical tenant UUID;
it selects an ordering boundary, never authority. Ownership and read/MFA policy
are checked independently on every request. Customer and platform-tenant
credentials cannot read this owner surface. Responses are private and no-store.
The endpoint stays outside the public OpenAPI and generated SDK contracts.

Each row contains its tenant name/status, pinned plan/revision and workspace-link
configuration, an existing customer-scoped doctor report, a reduced activation
projection, all-month work counts, and links to existing review/setup pages.
Doctor checks include feature grants, bindings and the current UTC-month start
allowance. They preserve `configured`, `observed`, `blocked` and `unverified`
distinctions. Live Auth membership and native qualification remain unverified.
No Auth user/admin token, invitation, input, result or signed review is included.

This is a bounded observation window on the responding API node, not a shared
database snapshot or admission reservation. All rows share a twenty-second
deadline; no partial page is returned as success after a failed read. Rechecking
the current assignment rejects an observed mid-read change. Crossing a UTC month
also requires a fresh request. Concurrent changes after any check remain possible.
Fetching one existing bounded customer page and retaining its first ten entries
does not skip the remaining entries; continuation uses the last emitted UUID.
The overview does not scan or imply totals for the entire customer population.

## Guided activation evidence

Reuse the latest retained onboarding status for each tenant. The projection
contains its setup identity/time, current-setup and access-configuration flags,
first verified customer workspace request, and first verified success admitted
after that setup. The existing onboarding store owns the evidence and its
retention semantics; this overview adds no activation writes or mirrored state.

- `not_recorded`: no guided setup record. Legacy/manual assignments may already
  have activity; absence of a record is not zero usage or failed activation.
- `awaiting_workspace_visit`: current setup exists without a recorded customer
  workspace request or verified success.
- `awaiting_first_success`: a customer workspace request was recorded, without
  a verified success after setup.
- `first_success_observed`: retained successful outcome evidence exists after
  the current setup. It is not a statement about current membership or health.
- `setup_changed`: the current assignment or access configuration differs from
  the setup. Historical success evidence stays visible and is labelled as such.

Onboarding sign-in is not a customer visit, uncertain work is not success, and
configuration observation cannot establish activation. No new inferred health
score or automatic repair is introduced.

## Work needing review

The separate read-only store projection counts retained, unwaived, unresolved
Product Control receipts for the owned app/customer across **all** admission
months. `unresolved` includes ordinary accepted/running work. `needs_review`
includes only unresolved receipts whose matching retained Operation is absent
or is outside accepted/running state, matching the existing attention semantics.
The earliest such receipt month selects the existing customer review page.

Missing result records remain reviewable; they do not imply failure, permission
to resubmit, or a charge. Resolved/waived receipts do not count. Memory and
PostgreSQL implement the same projection, with SQLC for PostgreSQL and no new
table, migration, admission rule, billing mutation or background worker.

## Owner and CLI behavior

The owner page distinguishes configuration observations, historical activation
and pending outcomes. Feature checks expand in place. Actions link to existing
readiness, onboarding or customer review pages; mutations still require their
normal review and confirmation. Next pages stay explicit and bounded.

The CLI provides readable and JSON forms, validates identity, order, bounds,
readiness/activation claims, work counts and local action scope, and performs
one GET per requested page. A successful overview read exits zero even when rows
have blockers; `product doctor` remains the per-customer configuration gate.
Errors are nonzero. Every report keeps native qualification `unverified`.

## Validation and release boundary

Portable tests cover page traversal, exhausted allowance, unavailable diagnostics,
changed assignments, cancellation, activation evidence, historical work review,
waivers/settlement, owner/customer separation, no-store, escaping and default-off
API/dashboard access. These checks do not replace the deployed Auth/browser,
email-provider and native exporter pilot. The service remains internal and off
by default until those separate rollout gates are met.
