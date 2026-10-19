# Controlled promotion rollout

New health-verified environment promotions require the existing admission flags
and a valid rollout approval. Ordinary deploys and unverified legacy promotions
keep their existing behavior. This runbook does not record a native pass or
authorize enabling production. The existing native partitions and remaining
supplemental cases must pass on the designated hosts first.

## Gather and review evidence

1. Run all six partitions of
   [environment promotion native acceptance](environment-promotion-native-acceptance.md)
   against the exact platform source revision. Retain the aggregate report and
   combined log. Verify CI provenance, source archive identity, all test outcomes
   and final leakchecks. A per-partition report cannot authorize rollout.
2. Complete applicable cases in
   [HTTP/worker qualification](promotion-health-native-acceptance.md) and
   [migration qualification](promotion-migration-native-acceptance.md). For `publication-crash-boundaries`, retain and review the new
   `promotion-publication` report and any applicable operator-control variants. Record a
   SHA-256 for the reviewed private evidence of every required gate in ADR-20261019120034.
   Do not mark an unexecuted case passed because another partition passed.
3. Select explicit account/project UUID pairs and capabilities. Start with a
   small internal cohort. The approval and local policy must both include each
   exact pair. Every extension also requires `health`.

For `service_graph`, also run and review the optional
[frontend/API qualification](promotion-service-graph-native-acceptance.md).
Embed its unedited report at `capability_reports.service_graph` and bind all
three `service-graph-*` receipts to its log digest. Signing that capability
requires the matching `--service-graph-log`; basic HTTP approvals keep the
existing six-part evidence requirements.

Combining `--migrate --workers` additionally requires the signed `coordinated`
scope and reviewed `coordinated-migration-workers` and
`worker-replacement-resolve` receipts. Worker approvals require the latter receipt
even without coordinated promotion. Run `qualify_coordinated: true`, embed its
exact-source report at `capability_reports.coordinated`, and bind both receipts
to its log digest. Sign with `--coordinated-log` pointing to that reviewed log.
This also collects the service-graph prerequisite. It uses the existing
worker and migration boot flags. Independent approvals cannot authorize their
composition; see [coordinated application promotion](coordinated-promotion-native-acceptance.md).

Multiple workers additionally require `worker_set` in the approval and policy.
The existing worker boot flag enables that scope. Review the distinct
`worker-set-cutover`, `worker-set-rollback` and `worker-set-recovery` evidence;
the single-worker native suite does not establish those outcomes. The normal
worker and applicable coordinated/service-graph evidence remains required.

The operator signer is the trust boundary for evidence provenance and review.
The CLI checks the complete report inventory and matching log hash; it does not
reproduce every native probe or establish artifact provenance through a network
service. Keep report/log artifacts from the trusted CI run together.

## Sign away from APID

Create a signer on the operator's controlled signing machine:

```bash
gregalectl promotion-rollout keygen \
  --sign-key promotion-rollout.key --trust-key promotion-rollout.pub
```

The private key is a base64 Ed25519 private key in a new `0600` file. The public
key is base64 in a new `0644` file. Neither command output nor approval artifacts
contain the private key. Keep it off APID hosts. Existing output files are never
overwritten by key generation or signing.

Prepare approval input with this shape. Replace every placeholder with reviewed
evidence; this illustrative input cannot pass validation:

```json
{
  "schema_version": 1,
  "source_sha": "FULL_40_CHARACTER_REVISION",
  "issued_at": "RFC3339_REVIEW_TIME",
  "expires_at": "RFC3339_EXPIRY_WITHIN_SEVEN_DAYS",
  "targets": [{"account_id": "ACCOUNT_UUID", "project_id": "PROJECT_UUID"}],
  "capabilities": ["health"],
  "native_report": null,
  "gates": {
    "http-failure-variants": {"outcome": "passed", "evidence_sha256": "REVIEWED_EVIDENCE_SHA256"},
    "operator-intent-races": {"outcome": "passed", "evidence_sha256": "REVIEWED_EVIDENCE_SHA256"},
    "publication-crash-boundaries": {"outcome": "passed", "evidence_sha256": "REVIEWED_EVIDENCE_SHA256"},
    "retention-failures": {"outcome": "passed", "evidence_sha256": "REVIEWED_EVIDENCE_SHA256"}
  }
}
```

Embed the complete aggregate JSON object as `native_report`, without editing its
source revision, tests, observations or combined log hash. Add the extra gate
receipts from ADR-20261019120034 for each requested capability. A receipt digest must name
the evidence that was actually reviewed, rather than a placeholder or arbitrary
constant. Set an explicit short expiry, no more than seven days after issuance.

```bash
gregalectl promotion-rollout sign \
  --input approval-input.json --sign-key promotion-rollout.key \
  --native-log native-e2e.log --output approval-signed.json
```

Signing binds report bytes, scope, capabilities, supplemental receipts and time
window. It does not enable a feature or modify a running server.

## Stage policy and check readiness

Install only the public trust key, signed approval and local policy on the APID
host. Use regular files; symlinks, special files and group/world-writable inputs
are refused. Files and parent directories must be controlled by the operator.
Give the APID service account read access, for example `root:faas` and `0640`.
The local policy is deliberately unsigned so pausing or narrowing it needs no
signing key. It cannot widen the signed approval.

```json
{
  "schema_version": 1,
  "paused": true,
  "approval_path": "/etc/faas/promotion-rollout/approval-signed.json",
  "targets": [{"account_id": "ACCOUNT_UUID", "project_id": "PROJECT_UUID"}],
  "capabilities": ["health"]
}
```

Configure these paths in an operator-managed APID systemd drop-in alongside the
existing boot flags. Do not set the qualification-only harness flag:

```ini
[Service]
Environment=FAAS_PROMOTION_ROLLOUT_POLICY=/etc/faas/promotion-rollout/policy.json
Environment=FAAS_PROMOTION_ROLLOUT_TRUST_KEY=/etc/faas/promotion-rollout/trust.pub
```

Use the exact full revision stamped into the APID artifact. Clean Makefile
builds stamp it automatically; dirty builds stamp `unknown` and are blocked.
Verified source-archive builders must explicitly supply `SOURCE_REVISION` from
their verified archive identity. An abbreviated Git SHA cannot qualify.

Readiness is an offline check of the supplied revision, files and boot flags.
Supply the values for the APID being staged, not the CLI's own build revision:

```bash
gregalectl promotion-rollout readiness \
  --policy /etc/faas/promotion-rollout/policy.json \
  --trust-key /etc/faas/promotion-rollout/trust.pub \
  --revision "$qualified_revision" \
  --account-id "$account_id" --project-id "$project_id" \
  --enabled-capabilities health --require health --json
```

Exit `0` means the requested capabilities are ready for these inputs; `3` means
blocked; malformed command input exits `1`. The report shows all four
capabilities, including disabled flags, unapproved scope, expired or untrusted
approvals and missing supplemental gate IDs. A paused policy is expected to
report blocked. Omitting `--enabled-capabilities` reads the existing flags from
the CLI's process environment. The command does not inspect a remote APID's
actual environment or deployed binary.

After evidence review and the ordinary production deployment process, install
the files/drop-in, set only the intended existing flags, restart APID for its
boot settings, and resume the approved cohort. Check readiness again using the
actual deployed revision and settings. No approval, flag, or production cohort
was provisioned by implementing this gate.

## Pause, narrow, renew and revoke

```bash
gregalectl promotion-rollout pause \
  --policy /etc/faas/promotion-rollout/policy.json
gregalectl promotion-rollout resume \
  --policy /etc/faas/promotion-rollout/policy.json
```

These commands atomically change only the local pause setting and preserve Linux
ownership and permissions. Resume still requires valid signed evidence, active
boot flags and matching scope. Replace a policy atomically to remove targets or
capabilities. To expand beyond the approved cohort, renew time bounds or change
the platform revision, obtain a new signed approval. Keep old immutable evidence
for review; install replacements atomically at the configured path.

APID checks immediately before creating each new verified operation. There is
no last-known-good cache for policy or approval: missing or invalid files close
admission. A request already checked before replacement may commit. Pausing does not cancel operations
already accepted, including queued operations. Existing idempotent requests,
health work, abort, rollback and migration recovery remain available after a
pause, expiry, evidence removal or owner restart. Admission gating is not an
emergency cancellation mechanism for in-flight commands.

Public API blockers do not expose approval paths, trust configuration or other
cohort members. Use the local readiness report for operator diagnosis. Trust-key
rotation requires updating the public key and restarting every APID; approval
and policy replacement do not require a restart.
For multiple APID hosts, distribute a pause or narrowed policy to every admission
host. This is local operator configuration, not a cluster-wide transaction.

## Native qualification access

The existing isolated promotion harness sets
`FAAS_PROMOTION_ROLLOUT_QUALIFICATION=1`. APID requires Linux amd64/root, the
root-owned designated acceptance marker, KVM and a local private `faas_test_*`
database, excluding templates. Other hosts and production database names refuse
this mode. It never reports production readiness and must never be installed
in production configuration. The guarded runner still owns host quiescence,
bare-metal checks, source identity and final cleanup/leakchecks.

Destination secret changes require the additional `secrets` capability, enabled
by the existing `FAAS_PROJECT_RELEASE_SECRETS_ENABLED` boot flag. A health-only
or source-secret approval cannot authorize it. The signed approval must include
reviewed, source-bound receipts for `promotion-secret-cutover`,
`promotion-secret-rollback`, `promotion-secret-recovery` and
`promotion-secret-key-rotation`, in addition to the common verified-promotion
gates. Qualify real guest delivery, daemon restarts, retained rollback, envelope
rotation with old-key removal and final leakcheck as described in
[ADR-20261019120042](../adr/20261019120042-secret-aware-environment-promotion.md). No production
qualification was performed in the managed workspace.
