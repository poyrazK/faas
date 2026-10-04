# ADR-487: Signed regional runtime quota authority

Date: 2026-10-04
Status: Implementation in progress; durable regional admission integration pending.

## Context

ADR-486 reserves global app capacity but cannot by itself authorize a region to
admit instances. A regional request must use durable local authority, without a
directory lookup on the wake path. An outage, expired token, lost response or
zero-valued app cap cannot prove drain; existing scheduler paths interpret zero
as a fallback to the full plan.

## Decision

Directory apid signs Ed25519 quota grants only after the allocation's issued
marker commits. The signed grant binds the directory issuer, global owner/app,
allocation UUID and generation, canonical plan policy, steady and rollout
capacity, original cell/tenant/app identity, original API origin and immutable
issuance timestamp. Policy identities and origin/slug validation are shared with
the directory. New grants require current canonical plan limits. Retirement and
drain preserve historical entitlement snapshots so a plan-table change cannot
strand an already issued allowance.

Operator-configured public keys are pinned to one issuer and authority role.
Key IDs are public-key digests. A key cannot be both a directory issuer and a
regional receipt signer. Rotation may overlap multiple keys for the same issuer.
Protocol version, operation kind and key ID are signed with domain separation.
Bounded canonical JSON and raw URL-safe base64 reject unknown/duplicate fields,
ambiguous numbers, alternate encodings and cross-operation replay.
Timestamps are immutable identity fields, not leases or ordering authority;
generation and retirement UUIDs remain valid across wall-clock rollback.

Retirement repeats the complete original grant and immutable retirement UUID.
A region must persist its tombstone even if retirement arrives before grant
delivery. Reordered grants never reopen a retired allocation. Durable local
fences must remain explicit and survive restart; they must not be represented
only by a zero app cap. The regional drain owner may sign a receipt only after
closing all admission paths durably and observing zero counted instances. The
directory verifies the cell key and compares the complete original grant and
retirement identity before releasing its retained capacity. A signed receipt
authenticates that assertion; it does not itself establish the operational fence.

The current implementation provides the wire protocol and directory signing/
receipt bridge. Operator key provisioning/loading, regional durable storage,
all scheduler admission paths, regional HTTP transport and reconciliation,
organization billing, and native KVM failure/recovery qualification remain
required before runtime admission is enabled. The organization release gate
remains in place during this integration.

## Validation

Verify signature and audience tampering, role/issuer confusion, key rotation,
canonical encoding and message bounds, current versus historical policies,
issue-before-sign durability, signing failure retention, retirement-before-grant
identity and exact delayed/duplicate receipt recovery. Run the same directory
signing/receipt contracts against memory and PostgreSQL. Native regional tests
must prove durable fences and zero counted instances across restart, lost
responses, reordered delivery and every admission kind before enabling runtime.
