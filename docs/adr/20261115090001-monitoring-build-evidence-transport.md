# ADR-20261115090001 · Build evidence in deployment monitoring collection

- **Status:** accepted for implementation; operational activation separate.
- **Date:** 2026-11-15
- **Extends:** [advisory running-build evidence](20261115080000-advisory-runtime-build-evidence.md).

The deployment collection adapter does not request the new advisory build
evidence. Add optional `MONITORING_EXPECTED_ROLE` to the existing Python SSH
adapter. Empty/unset selects the existing command; only `single-box-v1` and
`control-plane-v1` may add `--expected-role`. Reject invalid nonempty values
before SSH with a fixed unknown configuration diagnostic. Keep the selected
release's absolute binary path, both active-pointer checks, strict host keys,
stdin-only bearer delivery, child-environment token removal and existing
transport deadline. Do not retry an unsupported CLI without the requested role.

Project the optional `runtime_builds` into an owned closed shape before saving
the artifact or composing Markdown. Validate requested release, role and
boundary binding; closed states/reasons and source status; job identities and
inventory/source state; original clocks and freshness; approved node names,
full commit/target digests and bounded safe version labels; full-set count
relationships and preview/envelope omission accounting. Drop unknown fields
at every level. Mirror existing Go bounds (64 KiB API evidence, 32 preview
items, 1000 expected compute nodes, 4096 samples per vector, 128-byte node and
version names, 120-second freshness and 30-second skew) and pin the mirrors to
`pkg/api/limits.go` with an offline test. Introduce no new policy limit.

Missing older report sections, older API failures and malformed optional build
sections stay explicit advisory unknown. An invalid optional section is replaced
with `invalid_advisory_evidence` while retaining valid health checks, confirmed
fault flags and the CLI exit code. Invalid JSON, broken mandatory report binding,
unsupported CLI/transport failure and report-envelope violations remain unknown
verification, as transport cannot establish the mandatory health result.

Keep the serialized artifact within the existing 1 MiB envelope. Remove advisory
build preview items first, preserving full-set counts and separately increasing
source-plus-envelope omitted counts and adapter envelope omission counts. If
metadata must also yield, replace the advisory section with unknown
`report_envelope`. Never discard mandatory fault evidence to make room for the
new optional section. If the mandatory report cannot fit, reject it with the
existing invalid-report diagnostic. Preserve private atomic writes.

Summarize advisory state, fixed source reason/status, selected role, full-set
counts, omissions and per-job evidence availability. Do not interpolate observed
node, target, version or commit labels into job-summary Markdown. Their approved
bounded forms remain in the private artifact. Source availability is distinct
from matching build identity and from health qualification.

Cover both roles, default omission, invalid input without SSH, all three health
exit states, initialization, unsupported CLI, older API and missing sections,
unknown-field redaction, malformed metadata, truncated full-set counts, source
availability, serialized size fitting and original-response preservation with
offline stub-SSH tests. Keep deployment workflow settings and role activation
with the operational owner.
