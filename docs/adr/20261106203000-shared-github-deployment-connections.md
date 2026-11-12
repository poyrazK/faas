# ADR-20261106203000: Shared GitHub deployment connections, unique sign-in identities

- **Status:** accepted
- **Date:** 2026-11-06
- **Amends:** ADR-1345 and ADR-20261101160300 (deployment connection ownership), ADR-050 (repository dispatch).

## Context

A customer who created a second Gregale account could authorize GitHub but
received `github_account_conflict`: `github_installations` had a global unique
installation index. That restriction confused deployment authorization with
GitHub sign-in identity. The project repository index and webhook's single
account lookup also prevented independent deployment of the same repository.

## Decision

Keep sign-in links unique on `(provider, provider_subject)`. A conditional
OAuth-link upsert that affects no rows returns `ErrConflict`; it must not
report success or move the existing sign-in link.

Store deployment authorizations by `(account_id, installation_id)`. Each
Gregale account completes the existing account-bound, expiring OAuth state
flow. GitHub's live personal-owner / organization-admin / permission checks
remain mandatory before storing credentials. Connecting an installation never
adds or changes a sign-in link, nor transfers any apps, secrets or billing.

Allow one repository project per `(account_id, install_id, repo_full_name)`.
Unscoped project lookups fail with `ErrConflict` if the repository has multiple
owners. Push and PR dispatch enumerates connected repository targets, groups
them by account, and applies the existing handler separately to each account.
Every handler still resolves that account's exact durable installation and
live app bindings. Project targets remain dispatchable after their last app is
removed. Multiple app bindings for one account are processed together once.
One account's failure does not suppress processing of the others; failed
deliveries retain the existing idempotent retry behavior.

Installation reconciliation runs once per installation and projects health to
all connection rows. GitHub uninstall, suspension, and repository revocation
apply to every affected account. Deleting a Gregale account only cascades its
own connection row. The migration preserves all existing rows and credentials;
rollback fails if shared rows cannot satisfy the old uniqueness
rules, instead of deleting connections.

Browser connection failures return to Settings / Integrations with a stable
error code. The dashboard explains the outcome, cause, and next action beside
the connection form. Provider diagnostics, codes, tokens, and OAuth state are
never copied into that URL. JSON clients retain RFC 7807 status and code.
Declining authorization is a distinct outcome and is checked only after state
validation. The installation picker remains an explicit selection step.

## Rollout and verification

Ship the dashboard recovery copy before the new browser redirects. All githubd
workers must support account fan-out before shared repository connections are
made available; an old worker still selects one target. The expansion builds
the replacement indexes concurrently and preserves global exclusivity. Only
the contract migration in a subsequent full release drops the global unique
indexes. Unit/expand-only releases retain those restrictions. Expansion adds preview
account ownership and recovery/verification tables; existing connections
continue to work during rollout. Retain
the legacy conflict code for an older database or daemon during the transition.

Regressions cover independent OAuth connection credentials, concurrent account
connections, login exclusivity, shared projects, ambiguous unscoped reads,
push fan-out and redelivery, account-scoped binding lookups, installation-wide
health and revocation, browser recovery, and the unchanged JSON contract.
Live acceptance uses two independently connected fixture accounts and the
GitHub deployment journey to verify a push reaches both accounts; no production
connection is moved or revoked to test this change locally.

## Connection management and delivery recovery

GitHub Checks use a stable, opaque account suffix in both their display name
and external identity. App checks also retain the app and deployment scope;
PR checks aggregate only preview sets owned by that account. The preview-set
primary key includes account ownership. Legacy writers populate ownership via
a root-app trigger during expansion, and a keyset backfill commits each batch
before validation. The global uniqueness constraints are removed only after
all githubd instances support account dispatch and account-scoped preview reads.
Existing branch-protection rules that name `gregale-preview` or other legacy
Gregale checks must select the new account-specific check names for that repo.

The browser can list its deployment installations, verify personal or
organization ownership metadata and current permission grants, select an
installation when binding an app, and remove a single account's connection.
Verification snapshots contain no credentials and cascade with that connection.
Account-wide disconnect atomically clears only that account's bindings and
project deployment edges and marks its previews stale. It preserves running
apps, GitHub App installation secrets shared by other connections, and all
OAuth login links. Named CSRF cookies separate account management from app
management. Durable install and binding reads override process caches; token
refresh is a compare-and-update, never an authorization-creating upsert.

The shared delivery inbox stores account-specific dispatch outcomes separately.
Dashboard skip explanations use those outcomes, never another account's result
or raw worker errors. Successful accounts keep their successful status while a
failed sibling account is retried using the existing enqueue idempotency keys.

The provider delivery monitor scans up to five pages within its one-hour
window. It automatically requests redelivery only after two minutes for
transport failures (0, 408, 429, and 5xx) on supported deployment/lifecycle
events. It excludes a GUID with any successful provider attempt or any local
inbox record. A durable claim caps retries at three per GUID across processes
and restarts, backs off between attempts, and limits each poll to ten requests.
Provider rate limits defer all redelivery requests durably. Authentication and
configuration refusals remain visible for operator repair rather than consuming
transport retries. Recovery does not fetch, rewrite, or invent webhook payloads.
