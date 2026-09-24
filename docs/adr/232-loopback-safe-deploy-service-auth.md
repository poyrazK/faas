# ADR-232 · Loopback service authorization for Safe Deploy

- **Status:** accepted
- **Date:** 2026-09-24
- **Decision:** Keep customer canary, recovery, and rollback API routes
  account-scoped. Give meterd separate canary-advance and recovery-action
  credentials for a narrow APID operator HTTP surface bound to loopback. APID
  resolves the deployment or globally unique app slug to its actual account,
  then applies the existing plan checks and atomic transition/audit handler.
  The operator routes are not mounted on the public API listener. Missing,
  short, equal, or half-configured credentials fail closed.
- **Why:** meterd walks in-flight canaries across tenants, but an API key
  authenticates exactly one account. A single service-account key silently
  fails to advance other tenants' canaries, even when the token is present.
  Granting that key cross-account powers on a public route would weaken the
  customer authorization boundary.
- **Consequences:** APID and meterd must receive the same two distinct random
  secrets (at least 32 bytes each) before Safe Deploy is enabled. meterd uses
  APID's loopback operator origin, not its public origin. Credential scopes
  separate step advancement from recovery/rollback. Idempotency keys and the
  persisted compare-and-swap continue to protect retried writes. Staging
  acceptance must advance canaries for two different accounts and prove a
  customer's API key cannot cross accounts. Production activation remains off
  until those checks pass.
- **Rejected alternatives:** One ordinary service-account API key cannot
  cross tenants. Bypassing account ownership on the public API route would
  expose a high-impact IDOR. Direct meterd writes to deployment/traffic tables
  would violate APID's customer-intent ownership and duplicate the atomic
  audit transition.
