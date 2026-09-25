# ADR-240 · Application acknowledgement of secret reload

- **Status:** accepted
- **Date:** 2026-09-25
- **Decision:** Opted-in workloads receive a mode-0400 guest-local secret
  revision file and a platform-owned metadata endpoint. After rereading
  `FAAS_SECRETS_FILE` and applying it to its own clients, an application may
  POST `{revision,status}` with status `applied` or `failed`. guest-init relays
  only that closed, non-sensitive report over its instance-bound VSOCK; vmmd
  checks that the revision still matches the live deployment and persists the
  outcome for the exact current secret versions and runtime.
- **Why:** Guest-init can observe an atomic file write and signal operation,
  but cannot know whether the workload parsed the file or updated its
  connections. An explicit app-owned report closes that observability gap
  without implying the platform can inspect process internals.
- **Consequences:** The report is an application self-attestation, not
  independent verification. It contains no secret values or free-form errors.
  A missing report is unknown; a stale revision is rejected and must be
  reread/reapplied; temporary host unavailability is retryable. The API keeps
  the app's acknowledged version separate from guest-init's latest projection
  version because a rotation can race the two reports.
- **Security:** The revision is a hash over public scope/key names and opaque
  delivery versions, not secret values. It is projected as a mode-0400 file on
  guest tmpfs owned by the app user. The acknowledgement endpoint is local to
  the microVM and vmmd supplies account/app/instance identity from the bound
  VSOCK rather than trusting request identity.
