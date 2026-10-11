# ADR-604: Alert-driven rollback after completed releases

Status: Accepted

Date: 2026-10-05

## Context

ADRs 588 and 589 recover active rollouts. A completed release has no active
rollout, so an alert raised shortly afterwards cannot initiate recovery. ADR-601
already separates historical rollback preparation, binding checks, routing and
service handoff completion.

## Decision

- Add `post_deploy_rollback_window_seconds` to app alert rules. Zero (the default)
  disables historical automation. A positive whole-second window of at most one
  hour requires action `rollback`. CLI add/update expose
  `--post-deploy-rollback-window 10m`; updates can explicitly set `0`.
- Record a predecessor at deployment admission only when one completed release
  serves all traffic in that scope. PostgreSQL records it under the app lock;
  memory mirrors the selection. Do not backfill older releases by guessing.
  At fire time pin the sole completed serving release and its recorded, older,
  retained predecessor. Active rollout capture retains priority. Missing lineage,
  mixed traffic, ambiguous scopes, absent targets and expired windows fail closed.
- APID rechecks enabled rule, exact pair and current recovery window while
  committing intent. It preserves artifact/attestation and API contract checks.
  Commit one historical rollback operation (UUID equals fire UUID), preparation
  notification, attributed intent audit, release claim and alert receipt together.
  Different fires cannot initiate a second automatic historical rollback for the
  same release. Deleting a rule cannot erase that release claim.
- Reuse checked rollback preparation and the existing scheduler/imaged readiness
  path. Binding checks use fresh evidence for the exact recipient; workers run no
  probes. Completed canaries qualify as stable current releases while incomplete
  canaries remain excluded. This feature adds no instance writes or VM calls.
- Once accepted, rule disable or elapsed recovery window cannot strand cleanup.
  Read durable operation progress after restart. Project preparation, readiness,
  blockers and routing through the existing alert ledger. Completion requires the
  exact operation, routing audit and restored traffic; services additionally
  require the matching promote handoff, gateway acknowledgement and drain finish.
  Commit the attributed completion audit with the terminal receipt exactly once.
- Restored historical targets and earlier automatic recovery recipients cannot
  become another automatic rollback candidate. New deployments remain eligible
  when they have their own recorded predecessor and pass the same guards.
- Extend GET-only `alerts actions --wait` to pin historical kind and operation
  identity and require committed completion. Synchronize additive OpenAPI and
  Go/Node/Python receipt models. Public requests cannot supply internal grants.

## Validation

Memory/PostgreSQL parity covers atomic duplicate intent, exact lineage, opt-in and
window expiry, newer release rejection, wrong/expired binding grants, durable
blockers, service routing/draining barriers, concurrent completion and prevention
of rollback chains. API coverage exercises replacement-APID recovery through the
existing readiness worker and fresh target evidence. CLI tests exercise operation
pinning, incomplete completion and duration boundaries. Existing active rollback,
historical rollback, service binding and CLI regressions remain required.

Native x86_64 Linux KVM acceptance remains pending from ADR-601. The VM lifecycle
implementation is unchanged by this feature.
