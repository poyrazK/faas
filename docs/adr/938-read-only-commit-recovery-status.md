# ADR-938: Read-only Commit recovery status

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-936, ADR-937

## Context

A recovery apply operation stops on an ambiguous write. Operators need a bounded
way to inspect current delivery evidence while retaining the uncertainty of that
specific request. A changed routing generation or a completed consumer cannot
identify which operator's request caused the change.

## Decision

Add read-only CLI and Go, Node and Python SDK recovery-status helpers accepting
the reviewed plan and saved apply result. Validate source ownership, original
publication identities, subscriber selection, duplicate identities and saved
attempt statuses. Result items may cover a subset of the plan; observations
cover only those result items. Copy inputs, ignore saved aggregate counts, and
never follow saved action URLs or submit replay.

Each output item retains its saved attempt unchanged, fresh recipient evidence,
observation timestamp, current routing generation and its relation to the
reviewed generation. Current delivery status is sampled as completed, active,
failed, unresolved or unsupported_recipient. Missing, expired or replaced
acceptances remain unresolved. Read failures and budget/interruption gaps are
explicit read_error and not_observed statuses.

Retained latest generic replay execution takes precedence over original
execution, unless an acknowledged execution ID matches either retained
execution. That match is labelled matches_acknowledged_execution. All other
attribution is unproven. In particular, unknown saved attempts remain unknown
even if the generation advances or delivery completes. Saved inputs are editable
local observations, not signed evidence. No replay history search or implicit
retry follows missing execution evidence.

Limit subscriber observations to 1..50 per call, receipt pages to 1..20 per
item, and honor an overall context/timeout. CLI input files each use the existing
64 MiB report limit. Node/Python also perform a one-page-per-publication refresh
of the full saved selection to validate it before item observations, as apply
already does. Go validates that selection and source directly.

## Consequences

Operators can inspect ambiguous recovery outcomes without causing another
delivery attempt. Counts describe current observations only. Complete means
the loop considered every supplied attempt without read error, item-limit
truncation or interruption; receipt-page limits can still leave evidence unresolved.
it does not mean every consumer completed, every plan item was covered, or an
unknown request succeeded. CLI returns nonzero for unknown saved attempts,
unresolved/failed/unsupported observations or incomplete reads. Live evidence can
change immediately after sampling; production failure scenarios still require
qualification.
