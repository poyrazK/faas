# ADR-944: Transient recovery watch read retries

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-941, ADR-942, ADR-943

## Decision

Add opt-in CLI --read-retries N, Go WatchCommitRecoveryStatusWithRetry,
Node readRetries and Python read_retries options. Existing watch defaults
retain zero automatic retries. Accept 0..20 retries total across a watch,
without resetting the budget after a successful pass. CLI retries require
watch. The overall deadline includes reads, normal polling and retry waits.

Retry network/transport failures and HTTP 429, 500, 502, 503 and 504 only.
Cancellation, overall deadline expiry, authentication/authorization,
selection/identity validation, malformed evidence and other HTTP statuses
stop immediately. Aggregate all per-row failures so a transient first failure
cannot conceal a permanent later failure. Retry only when every underlying
failure in the bounded pass is eligible.

Backoff starts at the polling interval and doubles with each consumed retry,
capped at one minute. Honor available Retry-After seconds or HTTP-date values
as a minimum delay, even when longer than the backoff cap. Invalid/past
values add no delay. A server delay beyond the overall deadline waits for
that deadline; it never causes an early retry. No replay request is retried.

Retain the partial status from an eligible failed pass and send a detached
progress snapshot before waiting, allowing CLI checkpoints to persist it
under the existing ownership lock. Resume unread items before failed reads.
If validation failed before any receipt read, keep the fixed selection and
retry validation. Successful passes retain existing refresh/resume behavior.
An exhausted budget returns the failed partial status and error. Save failures
and callback failures stop monitoring and are not network retries.

Preserve Retry-After metadata in SDK HTTP errors where available. Go SDK
Problem errors retain status and the header, with typed fallback errors for
eligible non-Problem HTTP responses. Node Client typed errors retain the
header; non-JSON responses carrying Retry-After become typed status errors.
Generated Node service calls without the Client wrapper can only use metadata
present in their error body. Python detailed-response errors retain status
and headers; generated UnexpectedStatus errors can retry by status but do not
retain headers. Use detailed responses with default unexpected-status handling
for full header support. These additions do not change mutation retry policy.

## Consequences

Recovery monitoring can survive brief read outages while preserving unknown
request outcomes and bounded observation coverage. Evidence absence/replacement
remains explicit rather than being classified as a transient network failure.
Production failure-injection and rate-limit qualification remains pending.
No local tests are added or run; compliance registries remain unchanged.
