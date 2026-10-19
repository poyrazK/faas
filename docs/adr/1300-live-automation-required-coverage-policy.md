# ADR-1300: Required coverage for the read-only live automation gate

Status: Accepted

## Context

ADR-1299 compares retained suite selections. Both selections can omit the same
critical saved case and still pass.

## Decision

Add optional `--policy FILE` to the CLI gate. Version 1 JSON contains `version: 1`
and a nonempty `required_cases` set of exact saved-case names. Require every name
in the candidate selection, with missing coverage classified inconclusive (7).
Existing current-candidate failure precedence remains unchanged. Reject invalid,
unknown, duplicate or oversized policy input before authentication or API reads.
The centralized file limit is 16 KiB and the required set uses the existing
32-case suite limit. Report the local policy, canonical SHA-256 and missing names.
Hash canonical JSON with required names sorted, independent of whitespace/order.

## Consequences

CI can review and pin required coverage independently of suite selections.
Evaluation remains a single read; it neither discovers cases nor executes them.
Without a policy, existing strict_v1 semantics are preserved. Evidence age limits
and baseline promotion remain separate future work.
