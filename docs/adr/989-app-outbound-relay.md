# ADR-989 · App access to managed outbound integrations through a guest relay

- **Status:** accepted
- **Date:** 2026-10-11
- **Relates to:** ADR-250 (workload identity), ADR-424 (Runs outbound broker), ADR-958 (guest tracing bridge), ADR-988 (AI gateway), ADR-052/056 (control-plane mTLS and node identity)
- **Decision:** Give every app instance a loopback outbound endpoint, `GREGALE_OUTBOUND_URL=http://127.0.0.1:2774`, that reaches `outboundd` without a network route. guest-init relays each TCP connection byte for byte over a dedicated vsock port to vmmd. vmmd identifies the instance from the per-instance listener, mints a short-lived relay assertion, and splices the bytes to an mTLS relay listener on `outboundd`. Only `outboundd` parses HTTP. Requests use the existing `/i/{integration-id}/{provider path}` contract, so a stock SDK works with a base URL such as `$GREGALE_OUTBOUND_URL/i/<id>/v1`.
- **Why:** `outboundd` listens only on control-plane loopback. Apps reach it only when an operator routes a guest-reachable origin, and Runs use a buffered broker. Without a default path, managed integrations and the ADR-988 AI gateway have no users. Apps also should not need to fetch identity tokens or handle provider keys.
- **Consequences:** Apps get managed integrations with streaming, with no key, no token fetch and no egress rule. vmmd gains a byte splice but no new parser. `outboundd` gains a private mTLS listener, and the fleet PKI gains an `outboundd` server leaf and a vmmd client leaf.
- **Rejected alternatives:**
  - Running `outboundd` on compute nodes would put the database URL and the fleet credential key on every compute host.
  - Parsing HTTP in vmmd would put a guest-controlled parser in the only root daemon.
  - Routing through schedd would make the scheduler carry customer request bodies.
  - Opening a network route from guests to the control plane would break the egress default-deny (ADR-361) and the identical guest network world (ADR-009).
  - Extending the ADR-424 broker frames would keep its buffered request and response limits.

## Data path

1. **Guest.** guest-init listens on `127.0.0.1:2774`. For each accepted TCP connection it dials host vsock port 1032 (`VsockOutboundRelayHostPort`) and copies both directions, propagating half-closes. It does not read the bytes. guest-init sets `GREGALE_OUTBOUND_URL` in the app environment after customer values, like `FAAS_WORKLOAD_IDENTITY_ENDPOINT`.
2. **vmmd.** The per-instance Firecracker Unix listener for port 1032 identifies the instance; nothing on the wire selects it.
   - vmmd mints a workload identity assertion for the instance's app and account with audience `gregale:outbound:relay`.
   - It dials the configured relay target and writes one preamble frame: a 4-byte big-endian length followed by `{"assertion": "..."}`.
   - It then splices both directions without interpreting them.
   - The existing per-instance limit of 64 concurrent guest streams applies.
3. **outboundd.** The relay listener accepts only clients presenting a fleet-CA certificate bound to a registered compute node (node identity, ADR-056). Single-box hosts may use a Unix socket instead.
   - For each connection, `outboundd` reads the preamble within 5 seconds and verifies the assertion: signature, issuer, expiry, and the exact relay audience.
   - It then serves HTTP/1.1 on the rest of the connection. The verified app is the caller for every request on that connection.
   - Caller-supplied identity headers (gateway token, app ID, workload, execution and workflow identities) are removed before routing, so a relay request can only take the relay identity path.

The assertion authenticates only the start of the connection. A connection lasts until the guest or `outboundd` closes it. `outboundd`'s idle timeout closes unused keep-alive connections, and per-request deadlines still come from the integration policy.

## Policy

Relay requests follow the existing managed-integration rules: the app must be bound or operator-attached, and route policy, admission budgets, credential injection, caching, circuit breaking and AI usage accounting all apply. Application-authenticated (legacy token) integrations are not reachable through the relay, because the relay replaces the token with the platform identity.

Anthropic AI integrations (ADR-988) receive the managed credential in `x-api-key`, with any `Bearer ` prefix removed, instead of `Authorization`. Caller-supplied `Authorization` and `x-api-key` headers are dropped. This makes the Anthropic SDK work unchanged, using any placeholder key.

Qualification candidates do not get the relay. Their restore transport binds only the platform channels listed in ADR-568, so a candidate's connection to port 1032 fails.

## Operations

- **outboundd:** `relay_listen` (`tcp://host:port` with `relay_tls_*` certificate paths, or `unix:///path`).
- **vmmd:** `FAAS_VMMD_OUTBOUND_RELAY_TARGET` plus `FAAS_VMMD_OUTBOUND_CLIENT_TLS_*`. A remote target without usable client TLS is never dialed in plaintext: vmmd logs an error and keeps the relay closed, so a rollout before the new leaf is issued cannot stop VM hosting.
- **Firewall:** the control-plane firewall opens the relay port (8096) to compute CIDRs.
- **Unconfigured target:** when no target is configured, vmmd closes relay connections immediately, and apps see a connection error rather than a hung request.

## Deferred

- Per-binding token limits.
- Exposing the relay to Runs, which keep the ADR-424 broker.
- Rotating the assertion on long-lived connections.
