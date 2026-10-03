# CSA CCM v4.1 readiness

**Status: alignment draft.** This page and its companion JSON file provide a
Gregale-authored, high-level evidence index for the [CSA Cloud Controls Matrix
and CAIQ v4.1](https://cloudsecurityalliance.org/artifacts/cloud-controls-matrix-v4-1).
They do not reproduce CSA control text or the full control inventory, complete
the CAIQ, or claim a STAR submission, certification, or independent attestation.
See CSA's [CCM licensing information](https://cloudsecurityalliance.org/ccm-intellectual-property-rights-agreement)
before producing a control-by-control assessment. CSA's posted agreement refers
to CCM Version 4 and limits use to personal, informational, non-commercial
purposes; it also prohibits modification and redistribution and directs broader
use inquiries to CSA. The posted text does not explicitly name v4.1, so its
application to v4.1 artifacts and Gregale's commercial use needs written
clarification. Until then, keep any populated control-level assessment in
restricted evidence storage and do not publish a derivative or the control
inventory here.

The machine-readable index at
[`csa-ccm-v4.1-readiness.json`](csa-ccm-v4.1-readiness.json) groups existing
Gregale evidence by operational area. Each entry names an accountable party,
supporting parties, candidate evidence paths, the current evidence state, and a
concrete gap. The labels are Gregale's internal responsibility assignments,
not CSA's own control ownership determinations.

Gregale is accountable for the managed platform and its customer-facing
operation. The hosting provider is responsible for its underlying facilities
and infrastructure controls. Customers remain responsible for their workloads
and data-use decisions. Entries list supporting parties where responsibilities
cross those boundaries.

The recurring access-review procedure, read-only inventory query, and sign-off
template are now present. The first dated, signed review remains outstanding.
Other open evidence gaps include vendor-risk assessments for the hosting
provider and the pre-onboarding Neon managed-PostgreSQL backend; the supplier
register still mislabels the managed database as Hetzner, so the live service,
contract, and 30-day notice must be reconciled before provisioning is enabled.
Operator workforce and endpoint evidence and explicit customer-facing recovery
objectives also remain open. A restore drill is already recorded, but
its measured basebackup RPO was 176 minutes, so it should not be presented as a
short recovery-point commitment.

## Conventions

- `partial` means related artifacts exist but coverage or recurring evidence is
  incomplete.
- `inherited` means the control depends on an upstream provider and still needs
  provider evidence.
- `open` means the repository identifies a missing or unreviewed control area.
- No status in this document means that Gregale is certified or has completed a
  third-party assessment.

CI validates the readiness index's required areas, responsibility labels,
statuses, and repository evidence paths through `make standards-conformance`.

## Control-level evidence ledger

[`csa-ccm-v4.1-evidence-ledger-template.csv`](csa-ccm-v4.1-evidence-ledger-template.csv)
is an empty, Gregale-authored schema. It contains no CSA control references or
descriptions. Populate a restricted copy only after confirming the applicable
license; keep Gregale's owner, status, evidence, review date, and remediation
notes distinct from CSA source material. The public index can then link to
approved, non-sensitive evidence summaries without carrying the control
inventory.
