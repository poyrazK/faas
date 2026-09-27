# ADR-274: Additive HTTPS service-binding URLs

- **Status:** accepted
- **Date:** 2026-09-25
- **Decision:** Keep `GREGALE_SERVICE_<NAME>_URL` on the existing HTTP `*.svc.gregale:10080` endpoint and add `GREGALE_SERVICE_<NAME>_HTTPS_URL=https://<name>.internal` for each declared binding. The HTTPS value is an explicit canary; Gregale does not change transport defaults or retry a failed HTTPS request over HTTP.

## Why

ADR-272 and ADR-273 provide a private HTTPS listener and workload-scoped trust,
but deliberately leave generated URLs on HTTP while operators roll out the
listener, certificates, and guest trust. Applications need a stable way to
canary HTTPS without hand-constructing names or changing the environment
contract consumed by existing workloads.

## Contract

Standalone bindings, project-managed workloads, and previews receive both
variables from the same binding projection. The legacy `_URL` value remains
byte-for-byte unchanged. The companion `_HTTPS_URL` points at port 443 through
the standard HTTPS scheme and uses the binding's `.internal` alias.

The HTTPS variable is discovery, not a claim that every compute path already
has HTTPS enabled. Operators must provision the ADR-272 listener and ADR-273
CA trust before applications select it. If that endpoint is unavailable or
untrusted, the HTTPS request fails normally. No automatic HTTP downgrade is
performed. Removing a binding removes both generated variables.

## Consequences

- Applications can opt in one service call at a time by switching to the
  companion variable.
- Existing applications and generated legacy URLs are unchanged.
- Bindings consume two platform-owned environment variables; both are refreshed
  with dependency changes and removed when a dependency is removed.
- A future transport-default change requires a separate rollout decision after
  endpoint and client readiness are established.

## Rejected alternatives

- **Replace `_URL` with HTTPS now:** would break callers before all nodes and
  client trust paths are ready.
- **Silently fall back from HTTPS to HTTP:** hides partial TLS rollout and
  weakens the transport guarantee applications selected.
- **Require applications to assemble `<name>.internal`:** duplicates platform
  naming rules and bypasses the binding's generated discovery contract.
