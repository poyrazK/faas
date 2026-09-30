# ADR-377 · Local processes in development environments

- **Status:** proposed; implementation in progress
- **Date:** 2026-09-30
- **Decision:** Add a leased development routing overlay. apid owns session
  intent and authorization; an unprivileged bridge relay owns live laptop
  connections; gateways select the relay before VM selection for authenticated
  session traffic. Ordinary routing and Firecracker ownership remain intact.
- **Why:** Local breakpoints and local edit execution require a local process
  to participate in the remote application, beyond remote source synchronization.

Sessions identify account, developer, environment, intercepted app and explicitly
allowed dependencies. Laptop attachment and request credentials are distinct,
random, short-lived, stored as digests, and never exposed in inspection responses.
Only development resources are eligible. Trusted context must be verified at
every routing boundary; client-supplied headers cannot select a laptop.

The CLI opens an outbound TLS connection, forwards requests to a fixed loopback
destination and offers local HTTP proxies for allowed remote dependencies.
Transport must stream with backpressure, cancellation and bounded concurrency.
Disconnect fails session requests explicitly without retrying against the remote
app. Reconnect uses a new ownership generation to fence previous connections.

Session identity across remote app calls requires explicit framework propagation;
trace context is not authorization. Database access requires separately scoped TCP
forwarding. Selected webhook development replay preserves the existing durable
provider verification contract and is recorded separately from original delivery.

Completion requires API persistence and revocation, relay and CLI lifecycle,
gateway selection and dependency authorization, propagation helpers, inspection,
webhook replay, deployment wiring and end-to-end isolation tests. Session identity
unit tests alone do not establish feature completion.
