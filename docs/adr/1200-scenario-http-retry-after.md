# ADR-1200 · Scenario HTTP rate limiting and Retry-After

- **Status:** accepted
- **Date:** 2026-10-12
- **Decision:** Extend the existing scenario-scoped `http_status` fault to accept 429 as well as 500–599. Permit an optional integer `retry_after_seconds` from 1 to 30 for 429 and 503; manifests express it as `retry_after: 1s`.
- **Why:** Applications need to demonstrate bounded retries and respect dependency rate-limit delays. ADR-423 initially limited synthetic responses to 5xx, which cannot exercise 429 handling.
- **Consequences:** The managed internal service proxy emits a standard delta-seconds `Retry-After` header before its existing synthetic response. Namespace membership, authorization, percentage selection, match evidence, expiry, and cleanup remain in force. The parameter shares the existing 30-second fault-duration bound. No database migration or VM lifecycle change is needed; plans already persist JSON. Updated API/CLI/gateway versions are required for this new parameter. Headers are not added to ordinary responses. Clients choose their retry policy; Gregale does not retry on their behalf.
- **Rejected alternatives:** Arbitrary injected headers, HTTP-date values, public-ingress faults, and unbounded delay values broaden scope or make repeatable timing assertions harder. Malformed/truncated response bodies are deferred to a separate protocol fault.
