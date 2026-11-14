# ADR-20261112184625: Structured explanations of deployment log snapshots

Status: accepted

## Context

`logs --explain` summarizes errors for a person but explicitly rejects JSON.
Automation must parse terminal output or recreate the severity and pattern
rules. Build/deploy logs and runtime logs also use different sources, and a
closed stream alone does not establish that its selected window was complete.

## Decision

Support `logs APP --deployment ID --explain --json`, including the existing
`--release` alias and revision resolution. The new combination emits one
versioned explanation object and shares its severity/pattern model with text
explanations. Keep existing text routing, multiline build output, summary prefixes
and ordinary JSON log-record formats. JSON explanations require an
explicit deployment and exclude other selectors and log filters; local dry-run
query previews retain their behavior.

Verify deployment identity against the selected app before opening logs. Reuse
the existing status-based choice of build/deploy versus runtime logs. Resolve
revision handles and read metadata/streams within one 30-second budget. Bound
the read to 500 log records, including malformed records, and bound message
excerpts at 4096 bytes. Keep these operation budgets in `pkg/api/limits.go`.
These are client budgets, not new plan quotas. Redact active CLI credentials
and the shared redaction library's patterns before shortening or bucketing
messages. Explanations do not include raw JSON log rows.

Record persisted failure metadata separately from observed error counts.
Include source, deployment status observed before reading, coverage warnings,
entry limits, skipped records and explicit end/completion fields. Completion
requires a valid end frame and no gaps or malformed records. EOF without an
end, record cutoffs, degraded/error frames, unexpected events and transport
failures remain partial. Deduplicate warnings so repeated malformed records or
gap events cannot grow the report without bound. A completed retained window
does not prove all logs are retained or that the application is healthy.

Successful reads exit zero even for a failed deployment. Partial reads and
deadlines exit 3, transport errors exit 1, and cancellation exits 130, while
preserving observed counts. Metadata/identity failures before a snapshot use
the existing CLI problem format. Suggested commands inspect evidence only.

## Consequences

CI and agents can consume the same severity and pattern summary as a person,
without scraping terminal lines. The snapshot is bounded and read-only, with
explicit coverage limits and no automatic deployment retries or mutations.
Regression tests cover source selection, binding, text/JSON agreement, empty
windows, redaction before truncation, limits, malformed data and interrupted
reads. CLI journeys exercise the built binary against an isolated metadata/log
endpoint and add coverage to the existing deployed observability flow.
