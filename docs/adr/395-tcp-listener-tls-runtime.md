# ADR-395 · Public-edge TCP TLS enforcement

Status: proposed

## Decision

Project durable TLS policy into the accepting route and bind the supervised socket to that policy. Reserve connection credits before negotiating TLS. Only a successful handshake can proceed to target resolution; failed handshakes close the connection and release credits.

Before selecting a warm instance or admitting a cold workload, re-read app ownership, listener identity, enabled state, TLS hostname, and verified app-wide domain ownership. Reject policy changes, revocation, foreign apps, and environment-scoped domains. The public edge decrypts the stream; workload transport receives plaintext and no certificate key.

The public gateway opens an optional anchored file provider from `FAAS_TCPD_TLS_CERT_DIR`, requiring an absolute directory and restricting filesystem permissions. A malformed directory fails startup with a sanitized error and closes already-open dependencies. Close the provider on shutdown. An absent provider cannot serve termination listeners; passthrough remains available.

This runtime must precede customer TLS API exposure. Certificate issuance and edge readiness observations are separate integrations. Gregale remains stateless and Linux/amd64; native acceptance needs a designated KVM host.

## Validation

TCP race tests cover pre-selection negotiation, failed-handshake credit release, trusted supervised TLS sockets, and ownership/policy rejection before warm lookup or cold admission. Public-gateway tests cover certificate-directory configuration. These substitute workload admission/forwarding and are not native VM acceptance.
