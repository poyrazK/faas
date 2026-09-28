# ADR-289 · Sidecar runtime secret reload

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Extend ADR-280's explicitly granted sidecar secrets with
  workload-scoped live refresh for long-running sidecars whose OCI image opts
  into `com.gregale.secret-reload-signal`. guest-init refreshes only that
  sidecar's `env_secrets` keys, writes to its own tmpfs projection, signals
  only that sidecar, and stores reload observations and optional application
  acknowledgements by runtime and workload. Main-image reload remains limited
  to single-workload deployments; init helpers and non-opted-in sidecars retain
  restart delivery.
- **Why:** Restart-only delivery is safe but may interrupt proxies, telemetry
  agents, and other long-running helpers. An app-wide reload identity would
  cross workload authorization boundaries and conflate independent outcomes.
  The existing sidecar grant is the correct scope for both data and status.
- **Consequences:** vmmd carries only authorized secret key names in the
  workload roster; ciphertext is unsealed at wake and values remain in the
  instance-scoped sidecar env file. At runtime, the host resolves requests
  against the live deployment scope and the requesting sidecar's persisted
  `env_secrets` map. Each workload receives a separate secret projection and
  revision file, and the sidecar acknowledgement endpoint is stamped with its
  workload identity. API and CLI status include `workload_name` for sidecar
  targets. These are trusted-workload self-attestations, not a hostile-workload
  isolation boundary; workloads share the guest kernel and VM boundary.
- **Rejected alternatives:** Reusing the main workload's secret allowlist or
  reload record would violate least privilege and mix status for distinct
  consumers. Automatically restarting every sidecar after each rotation
  remains available as fallback but can interrupt healthy long-running
  workloads unnecessarily.
