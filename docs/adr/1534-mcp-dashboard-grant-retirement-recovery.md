# ADR 1534: MCP dashboard grant retirement recovery

## Decision

Extend `/dashboard/mcp` with an account-wide, paginated grant retirement ledger
and browser forms for the existing `retry` and `acknowledge_manual` actions.
The ledger includes deleted applications independently of the application filter.
Its view model contains only identifiers, labels, states and retention times.

POST forms run behind authenticated dashboard sessions and their existing MFA
checks. Bound bodies before parsing and verify a named account-bound CSRF token.
Reject unknown actions and duplicate form fields. Manual acknowledgement also
requires explicit confirmation that the operator resolved the grant at the provider.
Adapt forms to the existing account-scoped API handler so atomic live-lease,
retention and terminal-state checks remain authoritative. Never set
`allow_active_grant_revocation`. Successful actions redirect to the refreshed
ledger; errors retain the API status and safe problem response.

A retry queues revocation work, not credential renewal. Hide retry for awaiting
issuance or records marked issuance unknown. The API additionally refuses retry
without retained tokens and a revocation endpoint. Manual acknowledgement records
operator confirmation and discards sealed material; it cannot claim provider
acknowledgement or reconnect a role.

## Scope and follow-up

The existing guided authorization accepts only CLI loopback callbacks. Browser
reauthorization requires a separately designed hosted callback with session
binding and provider registration compatibility; do not bypass loopback validation.
There is currently no manual renewal-retry API. Reauthorization and renewal
remain CLI/operator recovery steps pending those contracts.

## Validation

Dashboard rendering tests cover explicit provider confirmation and suppression
of retry for unknown issuance. Authenticated handler tests cover missing CSRF,
invalid actions, account isolation, token-free uncertain issuance, successful
manual resolution and repeated terminal actions. Existing operations dashboard
ownership, pagination and safe-projection checks remain in place.
