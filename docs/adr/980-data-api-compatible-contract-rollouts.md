# ADR-980: Owner-approved compatible Data API contract rollouts

- **Status:** accepted
- **Date:** 2026-10-11
- **Decision:** Extend ADR-979's opt-in guard with an optional owner-configured
  `DATA_API_ACCEPTED_CONTRACTS` JSON policy. Version 1 binds explicit accepted
  fingerprints to one current fingerprint. The runtime validates and captures
  the policy at startup; a stale policy prevents startup before engine launch.
- **Why:** Exact matching rejects additive changes and impedes gradual client
  rollouts. Owners need to accept selected older contracts without changing
  client pins, bypassing authentication or automatically accepting future DDL.
- **Consequences:** `data-api diff --accept-contract FILE` emits a policy only
  when old-to-current comparison has zero structural breaking changes. Approved
  breaking changes do not qualify. Each additional old version needs a direct
  comparison with the same current version. Exact matching remains the default.

Acceptance keeps JWT authentication, RPC allowlists, grants, RLS, fixed request
logging and the qualified OpenAPI base path. It neither advertises the older
fingerprint as current nor translates responses to an older schema. Remove the
policy and refresh to revoke acceptance; every serving change requires review
and a new policy. Application behavior, policy semantics, defaults and function
bodies still require application tests. The owner can configure this policy,
so structural comparison is a tooling guard, not a cryptographic attestation.

No service, quota, management endpoint, snapshot format or VM owner changes.
The immutable-startup observation and out-of-band DDL limitations of ADR-979
continue to apply.

Local development accepts the policy through `--accepted-contracts FILE`.
Policy edits and migrations share serialized reloads; stale or unreadable policy
sources pause data access and preserve the database, session identities, saved
contract and types. Saved requests may specify `expectedSchemaFingerprint` to
verify mixed-client acceptance, rejection and RLS in local CI. Neither local
replay nor the inspector silently drops an expected fingerprint.
