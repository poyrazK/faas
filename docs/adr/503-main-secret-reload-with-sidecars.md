# ADR-503 · Main-workload secret reload with sidecars

- **Status:** accepted
- **Date:** 2026-10-03
- **Decision:** Extend ADR-222 and ADR-338 to allow the main workload to opt
  into secret reload in a deployment containing companions. Before starting
  its supervisor or poller, guest-init prepares the main workload's existing
  tmpfs projection and revision file. Main fetches, reload observations and
  application acknowledgements retain the empty workload wire identity;
  sidecars retain their independent named identities and projections. Only
  the supervisor consuming a changed projection receives its reload signal.
- **Why:** Rejecting the entire deployment when the main image opted into
  reload prevented main-plus-sidecar workloads from using the application
  adoption gate in ADR-502, even though the protocol already separates
  workload receipts and sidecar grants.
- **Consequences:** vmmd applies the persisted main `env_secrets` grant without
  falling back to all secrets when any companions are declared. An absent or
  empty main grant yields an empty projection in that layout. Single-workload
  legacy all-in-scope delivery stays available. Main supervisor restarts and
  health probes use the current in-memory projection, including key removal.
  Reload/adoption and revocation target reporting mark an opted-in main image
  as enabled regardless of companions. Main and sidecar application receipts
  remain version-fenced and independent even when they consume the same key.
  Non-opted-in workloads and init helpers retain restart delivery. Application
  acknowledgements remain trusted-workload self-reports within the shared VM
  boundary. The existing runtime freshness and rotation gates still apply.
- **Validation:** Unit coverage checks grant isolation, empty/malformed grants,
  independent failed receipts, stale acknowledgement rejection after rotation,
  revocation targets, projection preparation and restart environments. Native
  x86_64 Linux KVM `test-metal` and `leakcheck` remain required acceptance for
  the guest lifecycle change; emulated/virtualized unit tests do not replace
  those gates.
- **Rejected alternatives:** Only changing inventory's support flag would
  claim a capability that the guest and host denied. Reusing a sidecar's
  projection or acknowledgement identity for main would mix permissions and
  application outcomes. Granting all scoped secrets to main in a companion
  deployment would break wake-time authorization.
