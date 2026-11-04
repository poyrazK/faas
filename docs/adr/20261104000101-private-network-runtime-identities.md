# Private network runtime identities and reconciliation ordering

Status: Accepted

## Context

An app can have several replicas, while every guest retains its snapshot-compatible
10.0.0.2 address. Binding the app's allocated private IP in every replica creates
an ARP conflict. Hashing network IDs into 24 bits also cannot guarantee unique
VXLAN identities. Concurrent intent replacement and reconciliation can publish
routes or readiness for an intent that has already been deleted.

## Decision

Persist a unique private source address for each instance and physical node.
Use a bounded hash of that pair as the runtime owner key, so moving a scheduler
row to another node cannot inherit an unfenced previous owner's identity.
Reserve the existing app address as a service address with one durable owner.
Only that owner binds
the service address and installs its ingress DNAT. The internal HTTP gateway
continues to balance requests across replicas; direct service-address traffic
reaches the owner. A five-second vmmd loop promotes an eligible existing replica
when the service claim becomes available. Qualification, quarantined and staged
instances cannot own the service address.

Claims have no timeout. Withdraw service DNAT, remove the address, then release
the claim. Complete VM cleanup releases runtime identities only after physical
network removal has been confirmed. A lost node must be fenced and its ownership
retired before another node can take its service address. This favors isolation
over automatic service-address failover during an unconfirmed partition.

Allocate VXLAN IDs in a durable per-region namespace, preferring the existing
hash when free and probing subsequent IDs on collision. Retain reservations
when networks or accounts are deleted: an unreachable node may still have its
old transport. vmmd resolves this identity before applying the fabric. Probe
VXLAN kind, local address, VNI, parent interface, UDP port and learning mode;
recreate drifted links and report readiness only for the expected identity.

Use shared PostgreSQL session advisory locks for complete reconciliation attempts
and intent writes. Peering scope is account/region; attachment scope is app.
Lock order is peering then attachment. Reload intent after acquiring the lock;
status updates also compare the expected attachment definition. A pinned direct
connection carries callback queries and existing organization access fences.

Static egress aliases retain separate desired authorization and applied-kernel
state. Failed removals remain pending. Label owned addresses on br-tenants so
retries can rediscover them after daemon restart. Revocation of authorization
does not wait for successful address deletion.

## Consequences and rollout

Apply the expand migration first. Stop private-network wakes and drain all
legacy private-network guests before upgraded nodes can allocate runtime claims.
Upgrade the control plane and every vmmd in a region, then resume private-network
wakes and reconciliation. Mixed older controllers do not honor these locks;
older nodes still bind app addresses and use hashed VNIs.
No protobuf fields are removed. Uncontended VNI assignments preserve their old
values. Network/app deletion can conflict while a service owner remains; finish
compute cleanup first. Address capacity now includes one address per live VM
plus one stable address per attached app. Tombstone reservations require explicit
operator retirement after every previous node has been fenced; they are not
reused automatically.
