# ADR-158 · Per-action CSRF cookies on multi-form dashboard pages

- **Status:** accepted
- **Date:** 2026-09-05
- **Decision:** keep authenticated CSRF envelopes bound to one action and one
  account, while allowing a form to select a dedicated sidecar cookie name.

## Context

Issue #248 identified three dashboard forms whose POST routes were never wired.
The account page already renders account deletion, GitHub connection, plan
change, and one API-key revoke form per key. The existing double-submit helper
uses a single `faas_csrf` cookie, so only one independently action-bound token
can match that cookie on a page render. Reusing the account-deletion token for
key revocation would allow cross-action replay and violate the action binding
that the CSRF envelope exists to enforce.

## Decision

`pkg/middleware` gains additive `IssueForAuthenticatedNamed` and
`VerifyAuthenticatedNamed` helpers. They retain the same sealed envelope,
constant-time cookie/form comparison, ten-minute TTL, and account binding as
the existing helpers. The verifier reads only the caller-supplied cookie name;
it never falls back to `faas_csrf`. Existing callers and wire behavior remain
unchanged.

The first consumer is `POST /dashboard/account/keys/{id}/delete`, using
`faas_csrf_key_delete` and action `key_delete`. One token is shared by the key
forms on the page because authorization remains account-scoped; the route ID is
looked up with `(account_id, key_id)` to collapse missing and foreign keys to the
same 404. The customer must also type the displayed key prefix exactly. A
successful submission calls the same soft-revoke core as `DELETE /v1/keys/{id}`,
including key-cache notification and the `key.revoked` audit event.

## Consequences

- Several independently protected forms can coexist without weakening
  per-action replay protection.
- Named cookies are additive and use the same secure attributes as the existing
  dashboard CSRF cookie.
- API-key revocation becomes functional from the dashboard and cannot revoke a
  sibling or cross-account key.
- The plan-change and deployment-rollback slices of issue #248 can use the same
  helper with their own action and cookie names.

## Rejected alternatives

- Reuse `faas_csrf` for every form. The last rendered token overwrites the
  cookie, leaving the other forms unable to pass double-submit verification.
- Reuse one action token across destructive forms. This permits cross-action
  replay.
- Put all actions in one page-scoped envelope. This broadens every captured
  token to authorize all forms on the page.
- Move every mutation to a separate confirmation page. This preserves the
  single-cookie model but adds navigation and duplicate render handlers for
  each action.
