# ADR-505 · Atomic runtime secret snapshots

- **Status:** accepted
- **Date:** 2026-10-03
- **Decision:** Opted-in main and sidecar workloads receive
  `FAAS_SECRETS_SNAPSHOT_FILE`, a mode-0400 JSON envelope containing
  `{revision,secrets}`. Guest-init stages the envelope and the existing secret
  map and revision files in an immutable generation beneath a platform-owned
  directory, with each file owned by the workload user. One atomic generation-pointer replacement
  publishes all three paths. Only a successful publication advances restart
  state or permits a reload signal and success observation. Same-value fetches
  publish the new revision and unchanged values together. Remove retired
  generations after commit; cleanup failures cannot undo a committed update.
- **Why:** Publishing values before the revision allowed concurrent readers to
  pair new credentials with an old revision, even with a before/after revision
  check. A later write failure also left the projection ahead of restart state.
- **Consequences:** The existing secret-map and revision paths and formats stay
  available through platform-owned symlinks. Applications needing a consistent
  value/revision pair read the combined envelope in one operation. An already
  open descriptor retains its complete old generation after replacement. A
  path lookup racing retired-generation cleanup may retry. Each workload keeps
  its own image-resolved file owner, projection and acknowledgement identity.
  The Node starter prefers the envelope and fails closed if an advertised
  envelope is missing or invalid; it retains the separate-file reader only
  when the new environment variable is absent, for older guest versions.
  Initial preparation has an empty revision until the first successful host
  fetch, as before; applications acknowledge only a valid opaque revision.
  No API, VSOCK, SDK or database contract changes. Existing running guests gain
  the new projection contract on replacement with the updated guest-init.
- **Validation:** Cover partial staging failures, invalid publication, no
  signal on failed revision publication, concurrent reads, rapid rotation,
  revocation, same-value revisions, immutable open descriptors and restarted
  named-image users. Require native x86_64 KVM main/sidecar lifecycle tests and
  leakcheck. Node tests verify preference, bounded retry, sanitized failure,
  legacy compatibility and acknowledgements using the envelope's revision.
- **Rejected alternatives:** Three independent file renames retain partial
  publication. Reading a separate revision twice cannot establish which values
  were published between writes. Downgrading after a malformed advertised
  envelope would hide a broken consistency contract.
