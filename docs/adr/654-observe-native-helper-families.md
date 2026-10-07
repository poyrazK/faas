# ADR-654: Observe native helper families

Date: 2026-10-07
Status: Implemented locally; native lifecycle acceptance pending.

## Context

ADR-652 observes original helper leaders, including those outside their original
cgroup. A helper leader can exit while a child survives under another UID or
cgroup. Helpers previously inherited vmmd's session, so that session could not
identify orphan candidates independently of argv or the surviving leader.

## Decision

Production Linux helpers create a private session in the same fork that attaches
them to their pinned cgroup. Before releasing the execution gate, independently
read the original PID/start time, process group and session. The session and
process-group leaders must be that PID. Retain the original session ID in the
helper frame; a missing/changed observation cannot authorize the command. Old
frames remain readable without session backfill. Older binaries reject the new
field; rollback needs a compatible binary or a drained ownership journal.

Each fresh helper observation independently reads process PID/start time, PPID,
process group, session and unified-cgroup membership. Discover stable descendants
of exact original leaders, occupants of retained helper cgroups (including
unpublished and nested groups), and candidates in the retained private session
even when the original leader is absent. Follow stable parent relationships from
those candidates, requiring the child not to predate the observed parent.
Read the selected UID/cgroup scope and incarnation again, and repeat the family
census before and after the synchronous inventory consumer. Missing, duplicate
or changing evidence refuses the sample. Unrelated task changes do not alter the
family observation. Observation retains literal systemd escape characters in
kernel cgroup names without decoding them; the stricter effect-authority path
parser remains unchanged. Every helper backend must supply original-session
observation; an absent capability cannot bypass the execution gate.

Family observations retain original owner/helper generations, leader PID/start
time and the observed relationship. They remain unresolved and travel through
the existing request-bound inventory wire. A reused numerical session can create
an ambiguous candidate; it cannot grant ownership, signal authority or release.
The receiver rejects a hold match while its helper family remains observable.

Helper retirement checks family observations after retiring the original cgroup
and before publishing exit/resource removal. Even an already acknowledged frame
must pass the check on retry. Producers, replacement staging and parent resource
publication also recheck retired families using the caller's context. Historical
authorized frames without original session provenance cannot publish absence or
permit replacement; natural disappearance cannot supply that missing provenance.

## Consequences and validation

This closes surviving-session, retained-cgroup and live-ancestry discovery gaps.
It does not prove completeness for unrecorded historical descendants that
changed both ancestry and session, or grant automatic retirement of escaped
tasks. A child creating a new session before its ancestors disappear can escape
this association; complete producer-family containment/observation remains a
prerequisite to regional activation. No session-wide kill or current-row
backfill is added. All regional heads stay frozen.

Portable contracts cover orphan sessions, renamed/UID-changed descendants,
unpublished/nested groups, reused PID candidates, duplicate owners, scope and
incarnation changes, inventory consumer rechecks, wire copying, gate ordering,
retirement uncertainty and unavailable historical session provenance. Linux
diagnostics observe an actual private-session orphan through procfs; they create
no guest or cgroup. Dedicated native Linux x86_64 KVM metal, leakcheck and fleet
recovery acceptance remains required. Full physical inventory, original row/run
binding, first-fence reconciliation, snapshot capture/pause/migration and latency,
shared global authorization/billing, independent regional provisioning/stores/
replication, and cached ingress serving every selected healthy eligible region
remain in the complete multi-region scope.
