# ADR-255: Public destination checks for outbound requests

- **Status:** accepted
- **Date:** 2026-09-25
- **Decision:** The default `outboundd` HTTP transport resolves a provider hostname immediately before each new socket connection, rejects the lookup if any returned address is not globally reachable, and dials a checked address directly. The URL hostname remains unchanged so TLS SNI and certificate verification still use the configured DNS name. The default transport does not use environment-configured HTTP proxies.
- **Protection:** The checks reject private, loopback, link-local, multicast, unspecified, shared, documentation, benchmark, protocol-assignment, translation, and other reserved address ranges. Redirects are already disabled by the gateway. A customer-facing origin-creation endpoint must also call `ValidatePublicOrigin` before persisting an origin; connection-time checks remain necessary because DNS can change after creation.
- **Limits:** This protects the default transport used by `outboundd`. Code embedding `NewHandler` with a custom HTTP client supplies trusted transport policy and can bypass this default. Private provider origins are unsupported by the default transport. Network-level egress filtering remains useful defense in depth against routing anomalies outside IP classification.
