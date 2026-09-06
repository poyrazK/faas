# ADR-164 · Container workload networking v1

- **Status:** accepted
- **Date:** 2026-09-06
- **Decision:** Workloads in one Gregale task share the existing guest network namespace and discover explicitly declared sibling listeners through platform-owned loopback environment variables. The variables are `FAAS_WORKLOAD_<NAME>_HOST`, `FAAS_WORKLOAD_<NAME>_PORT`, and `FAAS_WORKLOAD_<NAME>_ADDR`; names use the existing RFC 1123 workload grammar with `-` mapped to `_`.
- **Why:** Sidecars and the primary workload already run in one isolated namespace, but they had no stable endpoint contract. Loopback keeps the inner network identical across snapshot restores and replica moves while avoiding host networking or a new public-port surface.
- **Consequences:** The main workload always publishes its effective port. A sidecar publishes an endpoint only when it declares a port; init and worker sidecars may omit one. Explicit port collisions are rejected because workloads share one namespace. Cross-VM service discovery, UDP, host ports, and public multi-port routing remain future work.
- **Rejected alternatives:** Host-port allocation would weaken the tenant boundary and complicate node migration. Per-workload guest IPs would break the identical-inner-world snapshot contract. A DNS daemon is deferred until cross-replica discovery needs a control-plane-backed registry.
