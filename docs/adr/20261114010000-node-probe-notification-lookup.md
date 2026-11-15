# ADR-20261114010000 · Direct node probe-notification evidence

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-14
- **Extends:** [operator notification evidence](20261113233000-serving-probe-notification-operator-evidence.md).

Add GET `/v1/admin/obs/nodes/{name}/probe-notifications` and include the result
in `gregalectl obs node <name>`. A direct lookup must assess the selected node
independently of the bounded fleet preview, which may omit an alerting node.
Keep operator read authorization, MFA, operator allowlisting and read-only
behavior. Accept one stable node name and no query parameters.

Read the full independent inventory with two strict queries. Unknown, stale or
unconfigured intent leaves `expected` null and channels empty. Fresh configured
intent excluding the node yields `expected=false`, `not_applicable` and no
channel data; this is not a registry-existence assertion. Expected nodes receive
exactly two channel slots, heartbeat and webhook, using six server-side
node-filtered queries. Both phases share one pinned evaluation time and one
three-second deadline. Bound each scoped vector to four rows, reject foreign
nodes and reuse the fleet evidence assessment and coherence checks.

Keep fixed states, reasons and failure codes, nullable history, original run
clocks and historical-success semantics. Complete evidence can describe a
failed submission; it does not imply current provider availability or receipt.
Exclude credentials, raw destination URLs, arbitrary labels and provider text.
Perform no probes, sends or retained-history writes.

Expose the response through the OpenAPI contract and generated Node and Python
SDKs. Treat the CLI's additional read as optional, with fixed unknown notes for
older servers, unavailable endpoints and invalid data. Refuse redirects and
validate node identity plus the complete envelope before human or JSON output.
Preserve the existing node inspection's exit behavior.

Verify nodes omitted from the fleet preview, known and unknown expectations,
freshness, query failures, malformed and foreign evidence, shared cancellation,
authorization, MFA, read-only behavior, safe CLI output and SDK round trips.
Bind the exact filtered queries to a real Prometheus fixture. Production
convergence remains separate.
