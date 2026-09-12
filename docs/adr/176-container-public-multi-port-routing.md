# ADR-176 · Container public multi-port routing

- **Status:** accepted
- **Date:** 2026-09-12
- **Decision:** Persist an app-owned, bounded listener declaration in the
  app manifest. A named TCP listener is reachable through the reserved
  hostname form `<slug>--port-<name>.<apps-domain>`. The gateway resolves the
  selector before admission and reuses the existing per-instance vmmd
  forwarding path with the selected guest port. Unnamed TCP listeners use the
  deterministic selector `port-tcp-<port>`.
- **Why:** Container images and sidecars can expose more than the legacy HTTP
  listener. Customers need a stable public route for metrics, admin, or gRPC
  listeners without learning compute-node addresses or widening the guest
  network boundary.
- **Security boundary:** Only listeners explicitly declared in the app
  manifest are selectable. The selector is resolved against the app after the
  existing app authentication gates and before rate limiting or wake
  admission; unknown selectors return the normal not-found response. UDP
  declarations remain guest-only because the current public edge is HTTP/TCP.
- **Consequences:** The route is portable across replica placement and node
  migration because it carries only a logical listener name and the vmmd
  transport still owns guest dialing. Existing app and sidecar hostnames are
  unchanged. Host-port leasing, UDP ingress, and custom per-port TLS policy
  remain separate follow-ups.
- **Rejected alternatives:** Binding a host port for every listener would
  couple public identity to a compute node and complicate restore/migration.
  Routing by a customer-supplied request header would be forgeable and would
  not work for ordinary browser or load-balancer traffic.
