# ADR-1468: Opt-in automatic native GitHub lifecycle reconciliation

Status: Accepted
Date: 2026-10-14

## Context

Native GitHub status sync and ticket creation (ADRs 1211–1212) still require manual lifecycle pushes. Provider callbacks cannot safely resolve a Gregale issue without fixing-deployment evidence. A background integration also needs explicit authority and protection against changed groups, bindings, provider edits and uncertain writes.

## Decision

Add an owner-configured policy for each stored GitHub reference, exposed through GET/PUT `tracker-links/{link_id}/auto-sync` and `issues tickets auto-sync`. Require normal app read access for inspection; require the owner, Issues entitlement, deploy-write and session MFA for configuration. Disabled is the default. Enabling requires a policy revision and a fresh native provider snapshot already aligned with the canonical Gregale open/resolved state. Pin the canonical source identity and a fingerprint of the owned installation/repository binding and source group membership/split-rule scope. Disabling works without live provider access.

Run the worker in apid so customer intent remains under its owner. Poll due policies every minute, claim at most five per batch with `FOR UPDATE SKIP LOCKED`, and schedule successful or transient-error checks five minutes apart. Keep interval, batch and lease bounds in `pkg/api/limits.go`. Use 30-second durable claims and 15-second provider contexts. Recheck the claim, current owner eligibility, entitlement, reference membership, installation and repository visibility before credentials and writes. Configuration increments a revision and invalidates claims; stale workers cannot save over a disable or reconfiguration.

Gregale is the only automatic lifecycle authority. When the provider still matches the last confirmed snapshot and Gregale changes lifecycle, persist a pending desired state before using the existing native snapshot-checked push. If a response is uncertain or apid crashes, preserve that pending intent. A later matching provider observation confirms the outcome without another write. Never overwrite an unexpected remote state: pause with `conflict`. Pause ignored/unsupported states, revoked connections, regrouped source identities and owner/entitlement loss with a bounded diagnostic reason. Paused policies do not resume without an explicit aligned re-enable. A local change during an unconfirmed dispatch also requires triage unless the pending remote outcome can first be confirmed.

Delete policies with their tracker references. Retain only normalized status evidence and connection fingerprints, never credentials or provider error bodies. Persist the latest outcome, last check time and next due time for diagnosis. Configuration emits an audit event; worker outcomes remain in the durable policy record. Manual pull continues to require a fixing deployment through the existing Gregale lifecycle action.

## Consequences

This delivers automatic pushes, including verified Gregale resolutions and regressions, without provider-side callbacks or automatic inbound resolution. Checks are eventually consistent and bounded; a larger backlog delays work. Provider read/write and control-plane transactions are separate: a change or disable after the final check cannot recall an already-dispatched GitHub mutation. Claims fence local progress, not GitHub's API. Supported-state snapshots cannot detect a remote edit that returns to the same state between checks. No exactly-once delivery claim is made.

## Validation

Mocked provider/domain tests cover ordinary pushes, pending-intent recovery without repeated writes, remote edits, changed bindings/groups, ignored and unsupported states, disable during credential acquisition and provider snapshot changes. PostgreSQL API tests cover disabled defaults, reporter denial, scoped references, policy revision conflicts, concurrent worker claims, resolution pushes, disable fencing, conflict pauses, incoming merges that retain the canonical ID, legacy app tombstones and plan downgrades. CLI and Go/Node/Python contracts preserve the revision and native snapshot used to delegate future writes.
