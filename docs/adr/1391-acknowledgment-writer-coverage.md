# ADR-1391 · Declared acknowledgment writer recording coverage

Status: accepted

## Context

Recorded counters cannot distinguish zero traffic from an older writer that omits
metrics. A manually asserted recording boundary does not detect later restarts or
heartbeat gaps. Self-registration alone cannot establish an exhaustive inventory.

## Decision

Require deployment-declared, exclusive apid and schedd slots for automatic
coverage. Persist fresh process sessions, capability versions, canonical inventory
revisions and database-clock heartbeat leases. Serialize heartbeat updates through
a derived global coverage head. Reject missing, duplicate, unsupported, unexpected
or disagreeing live writers, and reset continuity conservatively after changes.

Read coverage and counters in one repeatable-read transaction. Cap completed-minute
windows at the least recent verified heartbeat. Automatic watch conditions require
the entire effective window within continuous coverage; an explicit manual
recording timestamp remains an operator override. Checkpoint policy binds the mode.

## Consequences

Operators must maintain an exhaustive inventory during scaling and rollouts.
Unlisted legacy processes cannot be discovered or fenced. Inventory changes and
restarts warm up a new window. Heartbeat errors fail coverage closed without
blocking deliveries. Records are bounded and retained for 48 hours; public health
exposes no slot identities. This protocol does not attest database integrity;
restore recovery requires restarting writers to reset their coverage epoch.
