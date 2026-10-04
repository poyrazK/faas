# ADR-504 · Workload secret reload restart consistency

- **Status:** accepted
- **Date:** 2026-10-03
- **Decision:** Main and opted-in sidecars share a prepared runtime-secret
  projection lifecycle. Before supervisors or reload workers start, guest-init
  resolves the projection owner against the workload's image and publishes its
  initial secret and revision files. The reload worker keeps that identity for
  changed and unchanged updates. Process starts consume the current state and
  never recreate or reset the projection.
- **Why:** A sidecar start could overwrite a revision already fetched while
  dependencies delayed startup, and every restart cleared the revision again.
  Its environment overlay also retained revoked wake-time credentials when
  the live projection omitted a formerly granted key. Reload-time UID lookup
  against the main root could make a named sidecar user's private files unreadable.
- **Consequences:** Sidecar restarts remove all granted secret keys from older
  environment layers before applying the current authorized projection, including
  removal of all keys. Non-secret overrides remain intact. An update received
  before the first process starts survives dependency waits and subsequent
  restarts. File ownership and mode 0400 remain stable for image-local users.
  Projection preparation errors fail before a reload worker or supervisor starts.
  The main empty wire identity, named sidecar identities, application receipts,
  host-side version fences and persisted positive grants are unchanged. No API,
  SDK, schema or migration change is required. Init helpers and images without
  reload opt-in retain wake-time delivery.
- **Validation:** Exercise revoked environment removal, current revisions across
  real process starts, changed and unchanged fetch ownership, opt-out and invalid
  preparation. Require native x86_64 KVM lifecycle acceptance and leakcheck.
- **Rejected alternatives:** Recreating files on every process start races the
  poller. Falling back to wake-time values for missing live keys resurrects revoked
  credentials. Resolving sidecar owners in the main image mixes image identities.
