# Declared outbound connection circuits

Implementation: ADR-201 and ADR-570. Native acceptance is pending; the
capability remains internal until complete-path and rollout evidence passes.

## Enable and inspect

Set `FAAS_EGRESS_CIRCUIT_BREAKER=true` in both schedd and vmmd operator
environment configuration. Empty, `false` and `0` disable it; invalid boolean
values refuse startup. schedd also requires the upstream probe feed. Enabled
vmmd requires its compute-node database configuration to read durable policy.
Customers opt in to individual declared upstreams through the existing
upstream circuit-breaker API. Threshold, sample and open-duration overrides
are applied by the scheduler.

Inspect vmmd `Stats.instances.egress_circuit_enforcement`: `enabled` reports
the node switch, `applied` reports completion for that instance, and
`desired_revision` / `applied_revision` identify pending delivery. Absence
means an older node; a logical scheduler `egress_circuit_state=2` alone is not
enforcement evidence. Update RPCs refuse disabled nodes and acknowledge the
applied revision only when every matching live/pending namespace succeeded.

`schedd_egress_circuit_reconcile_success{app}` distinguishes a completed whole-app
reconciliation (1) from pending DNS, store or node application (0). A logical
open/half-open scheduler gauge describes probe health. Per-instance vmmd stats
describe completed kernel application; neither establishes real connection
rejection without the native acceptance check.

## Covered connections

Each enabled app network has IPv4 and IPv6 sets before guest execution.
An open circuit rejects new TCP connections to its supported resolved
addresses and port with a TCP reset. Existing connections continue. A declared
host's current A/AAAA answers are refreshed each reconciliation interval,
bounded to 64 answers per upstream and 3200 targets per app. Oversized or
invalid DNS answers are reported as failed reconciliation, and prior known
targets are retained. Shared address/port destinations share rejection.
Arbitrary guest DNS caches outside this answer set, UDP, application HTTP
status codes, pooled-connection requests and background jobs without app
identity are outside this contract.

## Recovery and failures

schedd stores desired targets and a revision before fanout. A parked app keeps
its policy. Enabled nodes read it before new/restored app network readiness;
a failed read or failed nft seed prevents guest boot. Pending wakes also
receive updates. Each namespace replacement is one atomic nft transaction.
Revision fences prevent delayed RPCs from reopening a circuit already closed
by a newer revision.

Each tick re-pushes policy without re-counting unchanged probe samples. Probe
history is replayed at its real timestamps after scheduler restart. Removing
or disabling the final upstream writes an empty policy, retained for repair
after a missed close or restart. When fresh reads show probe evidence older
than four intervals, rejection is released. A database outage retains the
last committed policy and refuses unverified new boots. DNS failure preserves
prior known targets and leaves convergence pending.

Turning the node flag off requires restarting vmmd. Existing namespace
adoption/cleanup and rollout behavior must be verified on native hosts before
using this as a production rollback procedure. An operator setting change
alone is not proof that an existing kernel ruleset has changed.
