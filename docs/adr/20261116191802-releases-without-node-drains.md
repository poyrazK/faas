# ADR-20261116191802: Releases restart compute nodes in place, without draining them

- **Status:** proposed (owner direction 2026-11-16: "no drain happens on nodes")
- **Date:** 2026-11-16
- **Related:** ADR 20261017 (vmmd restart adoption), ADR-20261024085817
  (rollout base generation and drain completion), ADR-1736, ADR-472

## Context

A compute rollout drains the node before it installs the release unless the
candidate vmmd proves it adopts every live guest (restart adoption, on for
fsn-2 and fsn-3 since 2026-10-31). A drain moves every instance off the node:
a snapshot capture on the source and a restore on the destination, so every
app on the node takes a restore wake, and the rollout waits for it.

| Release | fsn-2 | fsn-3 |
|---|---|---|
| rc.345 | drained: runtime base generation changed | drained: same |
| rc.346 | drained: "candidate cannot adopt every live guest" | drained: serving vmmd did not hand off yet |
| rc.347 | drained: "candidate cannot adopt every live guest", 334 s of an 8.4 min activation | **no drain** |

Three triggers remain:

- **A. Refused guests.** `adoption-check` refuses the restart if any one guest
  fails the proof. fsn-2 refused on rc.346 and rc.347, and the same check,
  re-run by hand on 2026-11-16 against the same node, adopted every guest:
  the refusal was the state of some guest at that instant (an operation in
  flight, a teardown). The play printed only the top-level reason, which is
  empty for per-guest refusals, so neither rollout said which guest or why.
- **B. A changed runtime base generation.** The node cache, like shared
  storage, keeps one generation per logical base key and one marker. The
  candidate's pre-stage replaces both, after which the serving vmmd refuses
  every bring-up on that base (rc.290, ADR-20261024085817). So the pre-stage
  runs behind the drain barrier and such a release always drains.
- **C. The first rollout after a node turns hand-off on.** Once per node.

Host maintenance that reboots (kernel patching) drains by necessity and is not
a release.

## Decision

### A. Refused guests: retry, then move only those

1. **A1.** node_join runs `adoption-check` again while only guests are
   refused, up to 7 attempts 10 s apart (`faas_join_adoption_check_attempts`,
   `faas_join_adoption_check_delay_seconds`). A node-level refusal (a
   top-level reason: adoption off, a serving vmmd that cannot answer) stops
   at once. The play always prints the refused guests and their reasons.
2. **A2.** Guests still refused after the retries are parked one by one, then
   the check runs once more: if it now adopts every guest, vmmd restarts in
   place; otherwise the node drains as today. Parking uses the operator
   recovery intent the owning schedd already executes (`force-park`), written
   from the control plane like node_join's break-glass drain, under a
   dedicated system actor so the audit names the rollout. At most
   `faas_join_park_refused_max` guests (default 10) are parked; more than
   that drains, since parking a large share of a node is a drain anyway.

### B. Runtime base generations side by side

The node cache keys runtime bases by generation, so a candidate's pre-stage
adds the new generation beside the serving one instead of replacing it:

- Cache entries and markers are per (base, generation). vmmd boots, restores
  and adopts each guest from its own generation's file; new boots use the
  generation in its environment.
- Shared storage keeps publishing per generation; the logical key no longer
  stands for "the" generation of a base.
- A generation is garbage-collected from a node only when no live guest and
  no retained snapshot on that node references it.
- The pre-stage no longer needs the drain barrier, so a base-generation
  release restarts in place like any other.

B changes the cache layout, imaged's pre-stage and vmmd's base resolution, and
supersedes the barrier part of ADR-20261024085817. It ships in slices, dark
behind a switch, and gets its own design section before its first slice.

### C

Unchanged: it happens once per node.

## Consequences

- With A1, a transient refusal costs at most about a minute of retries instead
  of a 5–6 minute drain and a restore wake for every app on the node.
- With A2, a guest that cannot be adopted costs one park for that app, not a
  drain.
- Until B ships, a release that changes a runtime base generation still
  drains every node.
- Every refused guest is now in the rollout log with its reason, so the next
  refusal can be fixed at its cause.
