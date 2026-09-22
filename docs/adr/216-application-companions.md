# ADR-216 · Application companions without exposing an orchestration API

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** Expose bounded helper workloads as **application companions**.
  `companions` is the preferred manifest and deploy-request name; the existing
  `extensions` and `sidecars` spellings remain accepted and normalize into the
  existing deployment/runtime representation. A closed managed-preset catalog
  resolves operator-configured immutable image digests at deploy acceptance.
  Every instance exposes named, bounded tmpfs directories under
  `/tmp/gregale/companions/<name>` to its application and companions. A single
  long-running companion with an explicit port may set `primary_ingress=true`;
  the gateway enables it only when all traffic-bearing live deployments agree.
- **Why:** Telemetry collectors, database proxies, and custom reverse proxies
  are legitimate application dependencies, but exposing a general container
  orchestration model would expand Gregale's lifecycle, storage, networking,
  and scheduling contract far beyond those use cases. The existing runtime
  already supplies bounded helper execution, shared loopback networking,
  dependencies, health monitoring, graceful stop, and isolated resource
  controls. The remaining product gaps were naming, managed image ownership,
  transient socket/file exchange, and primary reverse-proxy ingress.
- **Consequences:** The two-workload-helper cap and stateless-image gate remain.
  Managed presets fail with `companion_preset_unavailable` when an installation
  has no qualified digest. Shared directories are instance-local memory only,
  disappear on cold replacement, and are never a persistence primitive.
  Mixed rollouts that disagree on primary ingress fail closed instead of
  bypassing the proxy. Existing stored JSON and guest wire shapes need no
  migration. Operators configure preset images in apid TOML under
  `[companion_images]`.
- **Rejected alternatives:** A general-purpose workload-group API was rejected
  because it implies arbitrary membership and lifecycle semantics. Mutable
  preset tags were rejected because they break deployment reproducibility.
  Host-path or persistent shared mounts were rejected because companions are
  stateless and those mounts cross the existing tenant storage boundary.
  Choosing primary ingress from only the newest deployment was rejected
  because traffic splits could bypass a security proxy.
