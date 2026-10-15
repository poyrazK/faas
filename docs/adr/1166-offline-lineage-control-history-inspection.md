# ADR-1166: Offline lineage control-history inspection

Status: Accepted
Date: 2026-10-13
Related: ADR-1163, ADR-1164, ADR-1165

## Decision

Add a CLI inspector for version-1 control-history artifacts, with optional
comparison against an earlier artifact. Operate entirely offline: no login, API
reads, state changes or file writes. Read regular files up to the existing
256-KiB export cap. Reject duplicate/case-aliased JSON keys, unknown or null fields,
missing required metadata, unsupported format/version and trailing JSON values.

Validate nonzero scope UUIDs, creation/retention timestamps, filters and the full
observed sequence boundary. The journal's contiguous full sequence means
through_sequence equals observed_receipts (0..128). Selected receipts must be
strictly ascending, within that boundary, unique by command UUID, within the same
scope/incarnation/retention, consistent in selection size and valid in original
outcome/actor metadata. Unfiltered artifacts must contain the entire contiguous
journal. Filtered artifacts may have gaps but every included receipt must match
the normalized filter. Do not infer absent legacy attribution or reject a file
because its server retention deadline is now past.

Summarize selected receipt actions, original outcomes, actor identities/types and
legacy unattributed counts, alongside scope, filters and observed/selected counts.
These are recorded observations, not present-day job state. Files are unsigned
and editable: validation establishes structural consistency, not authenticity or
completeness of a filtered selection's omitted records.

Comparison requires equal stable app/subscription/job identities, original
creation time, original retention and normalized filters. Treat --compare as the
baseline and --file as the equal-or-later target by sequence boundary; local
observation clocks and app slug hints do not establish order or identity. Normalize
UUID representations and equivalent timestamp offsets in memory before comparing
immutable receipts; never rewrite files.

Report unchanged records, added matching records beyond the baseline boundary,
changed receipts at an existing sequence, command UUIDs moved to another sequence,
and records missing from either expected overlap. New records inside the baseline
boundary are inconsistencies rather than additions. Differing filters/scope or a
backward boundary are not comparable and fail explicitly. Consistent comparisons
and valid inspection exit 0; record conflicts exit 1 with a report. Parse/validation
or comparability failures use ordinary CLI errors and emit no success report.
No new API, SDK, schema or registry entry is required.
