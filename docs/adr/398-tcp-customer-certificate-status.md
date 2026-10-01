# ADR 398: Expose observed TCP certificate evidence to customers

Status: proposed

The authenticated app-owned TCP status endpoint returns current listener intent and per-edge certificate observations. Account and app ownership are checked before observation reads. Read failures return a sanitized capacity problem. The existing read scopes, MFA policy, and rate limiter apply.

The response explicitly uses scope observed_edges. Freshness and listener-intent validation are applied at read time, so retained stale, disabled, or superseded observations cannot claim current readiness or expose a current expiry. No fleet-ready boolean is provided.

Canonical and embedded OpenAPI schemas define the response, and generated Node/Python clients preserve unknown evidence with absent expiry. The Go client and CLI expose the same endpoint; CLI TLS creation and policy changes retain the separate provision-then-enable workflow.

Validation covers authenticated routing, account isolation before storage access, missing listeners, empty evidence, readiness, disabled and stale evidence, error sanitization, real HTTP client transport, and human/JSON CLI output. Native VM qualification is separate and remains required.
