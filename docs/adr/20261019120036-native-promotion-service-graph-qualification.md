# ADR-20261019120036: Native promotion service-graph qualification

## Status

Implemented locally. Native KVM execution and final host leakcheck are pending;
production admission remains disabled.

## Context

The base promotion lane covers paired HTTP/worker operations, admission,
migrations and publication crashes. It does not establish that a dark frontend
reaches its candidate API through deployment identity, destination HTTPS bindings
and secrets, or that API failure restores the complete graph. Enabling basic
HTTP promotions must not require unrelated service-graph qualification.

## Decision

Add an optional `qualify_service_graph` workflow input to the explicit promotion
lane. Its seventh serialized partition runs fixture metadata and one native
scenario containing independent preflight refusal, rollback and successful
recovery admissions. Six source-built owner artifacts and one separate-tenant
caller use real builder microVMs. Native probes, clocks, controller checkpoints,
wakes and publication remain owned by their ordinary daemons. Faults affect
owned process/guest behaviour, never injected health evidence or timestamps.

The guest uses the canonical destination binding without forwarding a release
claim. It reports both deployment and guest instance identities, graph identity,
configuration generation, transport and secret-match booleans. Require API-only
regression to fail the frontend probe, complete-graph restoration, cold recovery
without a candidate snapshot, APID/vmmd/gateway/schedd restarts, terminal park/wake
and explicit forged-header/cross-tenant rejection. Artifact and publication
counts detect rebuilds or duplicate preparation. The shared restart helper
targets exact owned executables, validates sockets before killing, and retains
the original launch environment. Daemon liveness excludes only children recorded
as intentionally stopped by the harness, so APID can remain down during peer
recovery without hiding unexpected peer exits.

Keep the base six-part inventory unchanged. A separate source/log-bound report
uses scope `project-environment-promotion-only/promotion-service-graph` and the
exact two-test inventory. The common attribution/cleanup parser validates its
three runtime cases. CI requires this report only when explicitly selected;
missing, failed, skipped, mismatched or incompletely cleaned runs fail closed.

Add optional `capability_reports` to signed approvals. A `service_graph` decision
requires its exact-revision supplemental report; its three existing scoped
receipts must reference the same supplemental log digest. The operator signer
also requires `--service-graph-log` and checks its bytes against the report.
Existing health-only approval requirements remain unchanged. The trusted signer
still owns provenance review; report generation cannot sign or enable a rollout.

## Validation and limits

Portable checks exercise guest protocol/HTTPS/header handling, strict negative
evidence cases, signing/readiness scope, owned process fault selection and CI
partition/aggregate contracts. Native-tagged compilation and fixture metadata
check wiring without claiming KVM outcomes. Actual execution requires the
designated native acceptance host and guarded final leakcheck.

Local verification passed: rollout/guest/harness race tests, the full operator
CLI suite, acceptance-tagged APID build and fixture metadata, focused default
and native lint, 22 evidence-parser tests, runner/partition/aggregate contracts,
environment registry, APID size budget and ADR uniqueness checks.

The scenario proves deployment/graph selection across park/wake, without
claiming a specific snapshot restore mechanism. It does not qualify mixed
HTTP/worker/migration graphs or replace the remaining gates in ADR-20261019120034. See the
[acceptance runbook](../ops/promotion-service-graph-native-acceptance.md).
