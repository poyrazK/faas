# ADR-239 · Per-runtime secret reload observations

- **Status:** accepted
- **Date:** 2026-09-25
- **Decision:** Persist the latest guest-init secret projection/signal outcome
  separately for each active runtime and secret. `GET /v1/apps/{slug}/secrets`
  returns those non-sensitive observations and `gregale secrets list`
  summarizes their versions and failures. Existing latest-report fields remain
  for compatibility.
- **Why:** A single `last_runtime_reload_*` value is overwritten when another
  runtime reports, hiding partial failures during a rotation. Per-runtime
  records let operators see distinct runtime outcomes without exposing
  credentials.
- **Consequences:** Observations are fenced to the current secret version when
  written and are retained per runtime until the secret or runtime is deleted.
  The list query includes only active runtimes that have reported. A missing
  report is unknown, not evidence that a runtime lacks access; the result is
  not a complete fleet denominator. A successful guest-init signal still does
  not mean that the application applied the credentials. Application-level
  acknowledgement is deferred.
- **Security:** The table and API contain only secret key/scope, opaque version,
  runtime ID, timestamps, and closed guest-init outcome fields. No plaintext or
  ciphertext is added to status responses or audit events.
