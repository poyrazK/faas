# ADR-223 · Multiple long-running application companions

- **Status:** proposed
- **Date:** 2026-09-23
- **Decision:** Raise the deployment helper bound to five total entries: at most
  one `init` helper and at most four concurrently running `sidecar` companions.
  Keep the existing stateless-image requirement, one primary ingress, shared
  loopback network, dependency graph, startup/liveness probes, and managed
  companion API. A dependency edge with `condition=healthy` remains the explicit
  startup gate; no declaration-order or implicit readiness behavior is added.
  Every helper continues to declare its own bounded RAM and other existing
  resources, and scheduler billing/reservation sums every helper's RAM. Enforce
  the bounds at API validation, scheduler conversion, persisted JSON/layer
  constraints, host roster construction, and guest startup.
- **Why:** Real applications commonly need more than one long-running helper,
  such as a metrics exporter, log forwarder, and database or service proxy.
  Gregale already implements dependency ordering, probe-gated health, per-workload
  resource isolation, and additive RAM reservation. A hard, small cardinality
  increase exposes those existing capabilities without introducing a general
  workload-group or orchestration API.
- **Consequences:** The helper-cardinality portions of ADR-069 and ADR-216 are
  superseded. The global bound is five, including an optional init; the running
  companion bound is four. API schemas and generated SDK models advertise five
  helper entries. A database migration raises both deployment JSON and normalized
  sidecar-layer caps, including an upsert-safe trigger. Existing deployments and
  manifests remain valid. Billing remains `plan RAM + Σ(helper.ram_mb) +
  PerVMOverheadMB`, so adding companions never makes RAM reservation implicit.
  Probes and dependencies remain opt-in and unchanged; multiple companions do
  not gain a shared lifecycle, durable storage, independent ingress, or provider
  selection.
- **Rejected alternatives:** Keeping one long-running companion does not cover
  common telemetry-plus-proxy compositions. An unlimited count or plan-specific
  soft cap would weaken bounded resource, device, and metric cardinality. A
  general-purpose multi-container orchestration API would introduce lifecycle,
  networking, and storage semantics beyond this narrowly scoped expansion.
