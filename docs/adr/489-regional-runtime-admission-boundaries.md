# ADR-489: Regional runtime admission boundaries

Date: 2026-10-04
Status: Implementation in progress; durable capacity reservation and native qualification pending.

## Context

ADR-488 retains regional signed quota authority and disables its activation.
Existing wake caps interpret zero as the full plan. Instance inserts alone do
not cover warm resume, migration cancellation/lease expiry or the early resume
inside warm snapshot publication. A receiver must prevent these paths from
executing while its authority remains blocked, with no directory lookup.

## Decision

vmmd wires a startup-only runtime authorizer into its Manager whenever durable
state is configured. Ordinary runtime operations resolve the original durable
instance and compare its app identity before consulting the local quota head.
App-task operations verify their durable account/app/deployment pin, live lease,
cancellation state and restoring/running phase before checking the same local
app authority. The command dispatch rechecks after restore and tears down a
fenced guest before sending command bytes. Builder and disposable execution continue
using their existing dedicated claim protocols. Storage errors and identity mismatches fail closed. Nodes without a configured
durable store retain their existing independent-runtime mode; global quota
activation on those nodes remains unsupported until the regional owner
authorization transport is wired. An independent app without a quota stream
retains regional admission; an accepted quota stream explicitly denies runtime
execution until durable allowance enforcement is implemented.

Manager checks before wake resource acquisition, before snapshot restore or
cold boot, and before publishing the live instance. A fence arriving during a
boot prevents publication and uses the existing teardown path. Every manager
resume, including migration lease expiry and snapshot-error recovery, rechecks
local authority. Warm snapshots pass a host-owned check into the JailerVMM so
its early resume before storage publication cannot bypass the Manager.

This boundary does not prove drain. An operation may already be in flight when
the journal changes; its durable instance/task row and host runtime still need
reconciliation and verified teardown. No drain receipt signer or quota
activation is exposed. Durable instance/task reservations, scheduler rejection
before work is queued, shared tenant billing, quota consent, signed transport
and native Linux x86_64 KVM failure/recovery qualification remain required.

## Validation

Verify denial before host resource allocation, restore/boot fallback and live
publication; verify resumed warm and migration VMs cannot bypass an updated
fence; verify warm snapshot early and error resumes fail closed. Preserve
cleanup on denial, independent app behavior, builder/execution claim ownership
and immutable app-task identity. Portable mocks prove call ordering and retained
resources only. Native tests must prove no guest resumes, leaks or late boot
publication across fence, restart and lost-response races before activation.
