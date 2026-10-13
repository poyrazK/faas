# ADR-967: Delivery-health SLO monitoring

## Status
Accepted.

## Decision
Add optional CLI and Go SDK watch targets for routing/completion p95 latency, retry/dead-letter outcomes per second, and routing terminal-failure percentage. Require a positive minimum sample count and sustained breach duration. Targets are inclusive; zero disables a target. CLI defaults are 10 samples and one minute. Bounds live in API limits and are mirrored in the SDK.

Routing evidence covers bounded recorded outcomes, not all accepted events. Compacted routing history is unknown. Latency uses successful routes; failure percentage divides terminal failures by successful routes plus terminal failures. Expiration is already a terminal-failure subset. Retry traffic counts successful routes, terminal failures and scheduled retries; its value divides retries by window seconds. Routing error-budget burn is failure percentage divided by the configured percentage budget.

Execution evidence requires complete, untruncated retained history, no missing roots or unknown outcomes, and retained roots. Completion latency samples successful window completions; dead-letter traffic samples successful and failed attempts, with dead letters divided by window seconds. Current backends do not assert execution history completeness, so execution SLOs remain unknown. Insufficient traffic, invalid windows, inconsistent identity and unavailable evidence also remain unknown.

The evaluator reports window evidence eligibility, scope and sample counts. Eligibility does not mean a target is met. The watch additionally requires fresh, strictly advancing component observations. Breaches require at least two observations spanning the sustain duration. A clear observation resets timing; unavailable evidence or a sampling gap loses incident confirmation. Resume preserves incident IDs but restarts sustained timing. Checkpoints and alert records persist targets, minimum samples and sustain seconds and reject mismatched resume settings. Existing checkpoints without SLO conditions remain valid. Durable outbox and acknowledgement semantics are unchanged.

## Usage
`gregale events delivery-health APP SUB --watch --window 15m --slo-routing-latency 1s --slo-routing-failure-budget 1 --slo-min-samples 10 --slo-sustain 1m --timeout 10m --checkpoint alerts.json`

Additional targets: `--slo-completion-latency`, `--slo-retry-rate`, `--slo-dead-letter-rate`. These are local watch rules, not server-side notification subscriptions. No backend schema, ordering, replay, deduplication or retention contract changes are introduced.
