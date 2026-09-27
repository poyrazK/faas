# ADR-282 · Runtime revocation of delivered secrets

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** When a secret named by a live workload's explicit grant has
  been deleted, an opted-in workload receives the current projection with that
  key omitted. guest-init atomically publishes the replacement projection and
  forwards the workload's configured reload signal. Restart-only workloads
  retain the existing missing-secret error and restart delivery behavior.
- **Why:** Removing a secret from storage must not leave its previous value in
  the guest's current projection indefinitely. A refresh error preserves the
  old projection, so deletion needs distinct fail-closed behavior for workloads
  that declared a live-reload contract.
- **Consequences:** The host continues to select only keys in the persisted
  workload allowlist. A missing key is treated as revoked only when the main
  image or sidecar has a persisted reload opt-in. The runtime revision includes
  the sealed envelope digest as well as the delivery version, fencing a
  delete-and-recreate where the new row restarts its delivery counter. The
  application must remove the old credential from its own clients after
  rereading the projection; file replacement and signal delivery cannot erase
  process memory. A restart-only workload retains its current process value
  until it is replaced by a deployment that no longer grants the deleted key;
  a cold wake that still references the deleted key fails closed.
- **Rejected alternatives:** Returning `secrets_unavailable` leaves the old
  projection in place. Delivering an empty string would make a deleted
  credential appear present and could be interpreted as a valid value. This
  decision sends an omitted key only to a workload that already holds an
  explicit grant and opted into reload.
