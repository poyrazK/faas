# Durable entity qualification handoff

Status: **not run**. The source implementation and dashboard are local. Another
testing agent must collect the evidence below before enablement or production claims.
Record the exact commit and local diff digest: this workspace contains uncommitted
feature slices, so a commit ID alone does not identify the tested source. Record
host architecture, runtime/Firecracker versions, provider and test prefix, feature
gates, plan and timestamps. Store redacted command outputs, metrics and results;
never include credentials or candidate customer data.

## Source and API checks

Run the repository-required Go/API checks and affected Go, Node and Python SDK
checks. Cover durableentity export, recovery, journal, backups and validation source
cases; apid observed-store/metrics and isolated-registry and validator release-hook source cases; guest executor
and execution lifecycle suites. Check dashboard import and PromQL evaluation against
a test scrape. Confirm collector reuse, missing-state samples, bounded labels,
rejection/conflict/replay distinctions and uncertainty precedence. No payload,
object key, entity identity or credential may appear in metric labels.

## Dedicated private GCS qualification

Use the normal configured CLI/ADC identity on the authorized acceptance host and an
existing dedicated private test bucket. The provider harness is
`pkg/durableentity/live_provider_test.go`; select
`TestLiveDurableEntityQualification` with `GREGALE_ENTITY_PROVIDER=gcs` and
`GREGALE_ENTITY_QUALIFY=1`. Use `GREGALE_ENTITY_BUCKET` and standard ADC configuration;
read provider configuration and the examples README for required non-secret options.
The harness chooses a unique qualification prefix. Explicitly enable
`GREGALE_ENTITY_CLEANUP_QUALIFY=1` for cleanup coverage. A skipped test is not evidence.
The harness writes/deletes test data and does not replace the native platform flow.

| Scenario | Required evidence |
| --- | --- |
| Acknowledged commit, restart and independent read | Same version/data and durable receipt; no permanent local disk dependency. |
| Lost successful commit acknowledgement | Identical retry replays; no duplicate transition, version or outgoing intent. |
| Process death or provider partition before/during publication | Incomplete references never become committed state; uncertain outcomes stay uncertain. |
| Lease expiration and owner takeover | Late predecessor cannot publish state, receipt, alarm or outgoing work. |
| Stable request ID reused with altered payload/pins | Conflict; prior receipt remains intact. |
| Alarm/outbox handoff with lost bucket/SQL acknowledgement | Stable delivery identity, replay-safe acceptance and preserved current retry history; receiver dedup evidence separately. |
| Backup/restore with concurrent transition | Export checksum/scope enforced, obsolete restore conflicts; successful restore preserves current receipts, alarms and outgoing work. |

## Native isolated restore acceptance

Use dedicated native x86_64 Linux KVM hosts. Run required metal/leak checks and an
end-to-end API restore with gates enabled and a registered deployment-bound validator.
Verify fresh disposable scratch, no application environment/credentials or outbound
integration grants, no NIC/public/DNS/internal-service egress, encrypted persisted
payloads, plan/input/output limits and scheduler-owned teardown. Verify cancellation,
crash, queue timeout, malformed/truncated verdicts, missing/wrong-app/tampered bundles,
live deployment changes, stale owners and receipt replay. Confirm no fallback into
an application VM and no state publication after rejection. Record the existing
SQL deployment/bucket commit race as a limitation; do not claim it is atomic.

## Shared validator artifact delivery

Qualify ADR-947 publication/lookup with separate API, imaged and schedd processes.
Confirm no API restart is required after publishing, conditional creation and read
integrity on native GCS, no deployment rebinding, identical retries after lost content
or metadata acknowledgement, and no fallback to a local registry in shared mode.
Check missing-bundle refusal before prime VM allocation and initial live publication,
API promotion/canary/rollback recipient checks and ordinary serving wakes during a
validator bucket outage. Record consistent daemon gates, app allowlists, environment
contract delivery and private credentials/permissions. Explicitly enumerate enabled
release paths: direct store writes and separate project/environment publication paths
are not covered by a universal fence. No tests or live artifact uploads were performed.

## Built validators and project copies

Qualify ADR-948's ordinary source build and cache-hit paths with a reviewed
`gregale.validator.json`, selected SourceRoot and consistent builderd artifact settings.
Missing, duplicate, linked, malformed and oversized descriptors must publish no
binding; cancellation must not bypass checks. Verify deployment digest/availability
inspection without exposing source, credentials or state. Check promotion/clone
transfer before readiness, interrupted/resumed copies, dark publication, release graph
activation, previous graph rollback and missing predecessor bindings. Verify the
builder's per-daemon credential file delivery and that storage credentials do not
enter customer build or validator guests. Source cases exist but remain unrun.

## Evidence decision

For every row, record pass/fail/not-run, source revision, command/scenario, redacted
artifact path and remaining limitations. Quantify latency and provider request/upload
volume using a representative bounded workload; do not invent an SLO or equate
inventory averages with physical billing. Confirm metrics do not mutate state.
Only qualified capabilities may advance in STATUS. Missing live/native evidence
keeps the corresponding gates disabled; this document does not enable any gate.
