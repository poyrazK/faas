# ADR-165 · Container protocol-aware workload ports

- **Status:** accepted
- **Date:** 2026-09-07
- **Decision:** Preserve OCI `ExposedPorts` in the workload `app.json` contract as a bounded list of named TCP/UDP listeners. Guest-init publishes a loopback endpoint for each listener using `FAAS_WORKLOAD_<NAME>_<PORT>_{HOST,PORT,ADDR,PROTOCOL}` and keeps the v1 workload-wide variables for the first listener. OCI entries use deterministic names such as `tcp-8080` and `udp-53`.
- **Why:** Images frequently expose a metrics, health, or datagram listener alongside their HTTP server. Losing that metadata forces image-specific environment configuration and makes sidecar coordination brittle. The image manifest is already present in every cold boot and restore, so the contract can widen without a database migration or a new host-port allocation.
- **Consequences:** TCP and UDP may use the same numeric port because they are separate socket namespaces; duplicate `(protocol, port)` tuples remain invalid within a task. The public readiness/HTTP port remains the existing `port` field. Host DNAT, public multi-port routing, and cross-VM service discovery remain separate follow-up work.
- **Rejected alternatives:** Adding deployment-level port columns would require a schema and sqlc migration before the image metadata can be useful. Allocating host ports for every exposed listener would change the tenant boundary and complicate snapshot restore and node migration.
