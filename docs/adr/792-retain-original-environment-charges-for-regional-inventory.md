# ADR-792: Retain original environment charges for regional inventory

Date: 2026-10-11
Status: Implemented; focused Linux race validation passed. Native acceptance pending.

## Context

Regional startup inventory retains original physical permits even when their
instance rows disappear. The integrated ledger charges those holds against node
resources and shared application concurrency, but omits the original environment
index. A stage or production ceiling below the shared plan limit could therefore
be exceeded after a scheduler restart.

## Decision

Resolve serving-hold attribution from the original permit's account, app and
deployment through the deployment-owned runtime scaling reader, which validates
the original environment owner without loading runtime values. Validate
the returned ownership and scope. UUID spelling aliases identify the same row;
current deployment pointers and catalog slug replacements provide no authority.
Retain environment attribution separately for each original physical permit.
Advisory rows and distinct host incarnations remain independently charged.

Missing, deleted, conflicting or unreadable original ownership retains every
known physical and shared concurrency charge and closes startup admission.
Nonserving holds require no invented environment seat. Index initialization
includes retained original holds. Only accepted exact original-source stop
evidence permits releasing their captured environment charges; generic row
cleanup cannot release them or a replacement lifetime.

## Validation

Exercise production aliases, configured stage ceilings below shared plan limits,
UUID aliases, independently retained host epochs and advisory rows, rebuilt
indexes, recreated environment slugs, unknown ownership and exact-source release.
Portable tests do not establish native serving or recovery acceptance. The full
multi-region implementation and dedicated native KVM gates remain outstanding.

The focused inventory, recovery and dispatch race run passes all 21 test events
on the internal Linux node. It includes compact local UUID rows and canonical
permit UUIDs, and requires unknown-owner cases to reach the original owner
reader before refusing admission. The GCE node is a diagnostic environment;
this result does not qualify native KVM lifecycle acceptance.
