# ADR-483: Node-owned deployment handoffs

Status: accepted

Date: 2026-10-03

## Context

Builderd already includes its registered compute-node name in snapshot_boot.
The rootfs export remains on that node until imaged publishes the application
layer. Every imaged receives the PostgreSQL broadcast, but siblings ignore the
event. Returning nil for that skip lets both LISTEN acknowledgement and replay
completion close the shared outbox row without performing the handoff. An
owner restart or missed LISTEN delivery can therefore lose its recovery cue.

## Decision

Keep snapshot_boot node-local through durable delivery. Return the explicit
db.ErrNotificationNotOwned outcome on a sibling. The LISTEN path treats this
as an expected skip and leaves the outbox row untouched. Replay carries the
same outcome through Loop.HandleNotification rather than treating it as
successful handling.

Provide node-scoped claim, drain and replay methods. Wire imaged's replay
worker with the same FAAS_NODE_NAME as its handler. Filter sibling work before
leasing or incrementing attempts, including expired processing rows. Keep
FOR UPDATE SKIP LOCKED and the existing claim-token completion/failure fence.
Other channels remain shared even if their payload includes node_id.

The migration adds an immutable notification_outbox_target_node function and
an index over active rows. Its fixed-size digest key avoids PostgreSQL's index
key limit for oversized owner strings. Claims also compare the full identity;
a hash match alone never grants ownership. The function extracts a string node_id
from snapshot_boot JSON, with the same Unicode White_Space trimming as Go.
Malformed JSON and invalid field types remain claimable so the handler can
apply normal retry/dead-letter behavior. This supports PostgreSQL 15 and avoids
a persisted duplicate ownership field or a payload-format change.

An unnamed daemon or event without a node ID retains single-box/legacy
compatibility. If an unscoped or misconfigured replay callback still skips a
claim, release only that claim token, undo its attempt increment, retain prior
failure/backoff data and end the batch to avoid immediately reclaiming it.
A stale release cannot overwrite a newer lease or a completed handoff.

## Consequences

A sibling cannot acknowledge owner work while the owner is offline or midway
through handling it. The returning owner can replay the original row. Unrelated
shared handoffs and legacy consumers retain their existing delivery semantics.
An unavailable named owner leaves its work pending; this does not transfer or
copy its local export to another node. Existing deployment reconciliation and
operator recovery remain responsible for a permanently lost node.

Apply the migration before upgrading imaged. Upgrade all named imaged daemons
to obtain the fleet-wide protection; older daemons still acknowledge skips.
The migration adds an index with the repository's normal transactional DDL;
schedule it with consideration for an active queue on a large installation.

## Verification

TestNodeScopedClaimsSkipSiblingBacklogAndKeepLegacyWork checks routing beyond
one batch of sibling events and legacy/shared compatibility.
TestNotificationTargetNodeHandlesWhitespaceAndMalformedPayloads checks parsing,
normalization and poison-event isolation.
TestNodeScopedClaimsAcceptOversizedOwnerWithoutQueuePoisoning checks oversized
identities, and the migration test builds the index over existing poison events.
TestUnownedDeliveryReleasesClaimWithoutSpendingAttempt and
TestNodeScopedLeaseReclaimFencesStaleSkips pin skip/completion fencing.
TestTwoNodeConsumersDeliverOnlyTheirOwnHandoffs exercises concurrent consumers.
TestPgSnapshotBootSiblingSkipSurvivesOwnerRestart runs the actual imaged handler
and verifies one build and snapshot-prime handoff.
TestPgSnapshotBootSiblingCannotAckInFlightOwner holds an owner build while its
sibling receives the broadcast.
