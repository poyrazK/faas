# ADR-495: Regional runtime plan and purpose binding

Date: 2026-10-05
Status: Binding implemented; permit issuance, native qualification and activation pending.

## Context

A regional permit must bind the actual lifecycle operation, not just its app
and host. A Pro grant cannot authorize a Scale wake. A physical prime or task
permit also cannot supply serving concurrency. Snapshot priming uses the same
cold-boot transport as ordinary serving today, while worker deployment starts
remain running and must retain the serving contract.

## Decision

Compare the operation's instance, app, regional account, deployment, plan and
purpose with the complete placement. Ordinary execution requires a serving or
warm physical permit and an exact serving seat. Paused warm restore, snapshot
prime and app task require their corresponding physical kind and no seat.
Builder and disposable source execution retain their own claim protocols and
cannot supply regional runtime placement.

Carry an explicit host-only `prime_only` marker on the cold-boot transport.
The scheduler sets it for request/service snapshot primes; worker starts remain
ordinary serving and job deployments remain artifact-only. Prime accepts only
fresh cold boot, and cannot be restored, migrated, resumed through the generic
path or used for warm capture. Retain its immutable purpose privately on the
Manager instance. Forwarding namespace lookup excludes primes, builders,
disposable execution and app-task guests. Snapshot capture and teardown remain
available to the prime owner. The marker and quota metadata never enter the
guest AppSpec or snapshot's guest configuration.

Prime's scheduler capture skips the serving warm tier even if its warm gates
are open. Ordinary serving park continues to capture warm before init.

Destructive snapshot capture clears all resume-before-publication options.
This closes the remaining embedded resume path while preserving capture and
teardown for a fenced or prime instance.

Validate bindings before scheduler RPCs and before host allocation. Fill absent
regional deployment correlation from the pinned selected deployment and refuse
conflicting correlation. Production also requires placement whenever a durable
regional quota head exists, even if a future head were enabled; metadata-absent
ordinary traffic cannot fall through to independent admission. Pin the operation
plan to the original durable grant. Independent builder/execution claims retain
their existing separation. All regional heads remain frozen in this step.

This does not issue physical permits or serving seats, establish native resource
identity, verify stop or emit operational stop signatures. Those owners, signed
compute transport, retirement reconciliation, legacy inventory and dedicated
native Linux x86_64 KVM recovery/test-metal/leakcheck acceptance remain required.
The complete feature also retains explicit quota consent, global billing and
metering, app orchestration, and serving from all selected healthy eligible
regions as the routing default.

## Validation

Exercise invalid plans and cross-purpose placement before host work, immutable
prime purpose after caller mutation, prime capture/teardown, refusal of forwarding
and generic resume/migration/warm operations, and preservation of ordinary serving.
Verify complete scheduler/server identity delivery, pre-RPC rejection, worker
and artifact-only job deployment behavior, and durable-head rejection of missing
placement or a different plan. Run the affected portable lifecycle, scheduler,
wire and daemon regression suites with race detection and protobuf regeneration.
These checks do not substitute for the pending native acceptance gates.
