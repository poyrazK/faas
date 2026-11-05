# ADR-20261105115100 · Gate public raw ingress on production readiness and teardown

- **Status:** accepted
- **Date:** 2026-11-05
- **Amends:** ADR-20261104170000 (raw ingress), ADR-20261105120000

## Context

Public TCP/UDP weighted selection combined independently weighted production
and staging rows. Two 100% lanes made routing fail, and a staging-only app could
receive public traffic. A staging quarantine also withdrew production listeners.
Raw resolvers did not enforce configured reversible readiness. The default UDP
bridge used only the RPC context, so instance destruction left its stream and
helper alive until the client disconnected.

## Decision

Select the app's existing production routing lane before validating weights or
quarantine. Named production carrying traffic takes precedence over the legacy
default lane; dark named production retains legacy traffic. Staging never serves
public raw ingress. Use this projection in late deployment checks and public
listener lookup/inventories in both stores. Keep ordinary weighted selection and
established-session pinning within the selected lane.

Before returning a warm or newly woken target, load the selected deployment's
readiness configuration without the TCP routing cache. Require current ready
observations for configured primary-app and primary-ingress companion probes,
scoped to app, instance, wake and node. Auxiliary companion probes do not gate
traffic. Missing evidence, malformed configuration and unavailable readers fail
closed. Probe-free deployments keep their existing behavior. Filter unhealthy
replicas; when existing residents are all unready, refuse the connection without
requesting another scheduler wake. Reuse the shared traffic-readiness read timeout
and batch bound. Recheck current listener/owner intent and deployment quarantine
after readiness reads.

Register per-connection UDP forwarding in the existing instance session registry
before checking liveness. Propagate its cancellation to guest preparation, bridge
process and duplex pump. Returning the handler releases gRPC flow control; do not
wait for blocked Send/Recv calls before returning. Report instance teardown as
Unavailable, including when helper EOF races cancellation. Destroy, park and
in-place freeze use the same existing registry cancellation paths.

## Validation and rollout

Regression tests exercise mixed production/staging traffic, lane transitions,
quarantined staging and displaced legacy rows, both listener inventories,
readiness withdrawal/recovery, multiple replicas, cold wakes, source identities,
required companion probes, failed reads and deadlines. Lifecycle tests run the
actual TCP/UDP RPC handlers and Destroy path with a substitute namespace bridge;
flow-control tests exercise the production pump over gRPC. Native namespace and
Firecracker acceptance remains a dedicated Linux x86_64 KVM-host gate.

Deploy through the ordinary PR/release workflow. No schema or RPC migration,
plan limit change, or scheduler lifecycle writer is introduced.
