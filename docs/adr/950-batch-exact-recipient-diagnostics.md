# ADR-950: Batch exact-recipient diagnostics

## Status
Accepted.

## Context
ADR-949 rotates up to 50 diagnostic recipient reads per pass. Separate HTTP requests add overhead to large fan-outs while each acceptance guard must remain independent.

## Decision
Expose read-only POST /v1/events/recipient-blockers with 1–50 ordered queries and a 65,536-byte body limit. Validate every query before reading. Use the existing account-scoped exact lookup and its independent repeatable-read snapshot for each item. Duplicate queries are allowed and processed independently. Each response item echoes its query and contains exactly one result or stable read_error/read_timeout error. A shared five-second deadline bounds total storage work; remaining items receive read_timeout. No storage exception details are exposed.

Monitoring helpers send the existing unread, failed and refresh selections in one batch. They validate response cardinality, ordered identity and acceptance guards before accepting evidence. Missing or malformed evidence becomes read_error. Transport failures mark selected recipients read_error; reads continues to count recipient attempts rather than HTTP requests. Cursors advance for all submitted items, including item failures. Cancellation before submission preserves coverage; cancellation during submission preserves independent retry scheduling. Earlier retained samples retain their timestamps.

## Consequences
The HTTP overhead falls to one request per enrichment pass. The batch is not an atomic snapshot and does not imply handler completion, replay attribution, successful delivery or settlement. Older servers without the endpoint produce diagnostic read errors; no backlog-scan fallback occurs. Existing receipt monitoring and recovery behavior is unchanged. No migration or SQL change is necessary.
