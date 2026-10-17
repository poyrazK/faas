# ADR-1546 · Admit function migration capture and source recovery

- **Status:** implementation for qualification; native execution pending; production off
- **Date:** 2026-10-17
- **Decision:** Issue distinct, short-lived capture and recovery permissions for
  an ADR-1545 Node source with a measured runtime receipt. Keep destination
  adoption and production activation gated.
- **Why:** The serving receipt identifies the physical VM but does not grant
  fresh checkpoint or resume authority. Pool drainage and recovery must be
  authorized against current configuration, artifacts, security and ownership.

## Permission contract

The trusted vmmd source handler issues a capture permission through the state
store after pinning the original host instance. The transaction uses the existing
native snapshot-input and artifact approval fences, checks the current lease and
source wake, and commits capture keys, the source receipt and the permission
together before checkpoint effects. It writes no instance state. schedd remains
the sole instance-row writer.

Each permission binds its purpose, nonce, measured serving receipt, original wake,
lease token and creation clock, observed lease expiry, source start clock,
Firecracker version and all three immutable capture keys. Capture permission
expires by the lease, current artifact approval and existing admission TTL.
Recovery uses a new nonce and deadline after schedd's durable rollback fence;
an expired migration lease does not renew its old capture permission. Renewal of
the same lease requires a newly observed recovery permission. Exact retries reuse
the still-valid permission so they cannot revoke an in-flight recovery ACK.
Expired recovery permission receives a new nonce; host one-attempt accounting
still prohibits another application callback. Current input,
artifact, node incarnation, network and physical identity checks remain mandatory.

Permissions are internal state-to-host values, issued in the authenticated source
RPC. They do not expand the ordinary warm, park or migration grant protocols.
The host checks current durable permission before and after capture or recovery,
holds its egress-policy gate and bounds capture/pool recovery by the permission
deadline. Physical teardown retains its separate bounded cleanup context and may
finish after permission expires; it never authorizes another resume attempt.
Requests stay closed through the recovery ACK. A permission rejected before
effects grants no permission to checkpoint or resume. Ambiguous registration
retains the known no-effects source result and its durable lease.

Resource recovery still has one callback attempt. Duplicate cancellation reuses
the confirmed host result under current permission; it never replays application
work. Expiry, lost ACK or failure after effects seals admission and retires the
original VM. Unconfirmed cleanup retains allocator and scheduler reservations.
Metadata cleanup after schedd completes RUNNING or STOPPED needs no new resource
callback. Restart without the original host owner retains uncertainty.

## Qualification

Portable tests cover permission scope, expiry, policy/artifact/incarnation drift,
fence requirements, wrong parents and keys, duplicate RPCs, lost registration
acknowledgements and cleanup ownership. PostgreSQL tests exercise real locked
issuance/check transactions and replay-safe schema changes.

The native Node 22/24 SQL matrix must preserve normal measured boot admission,
witness all eight sessions closing before capture, and witness fresh probed pools
before recovery ACK. Cancellation and expired-lease recovery must preserve the
original VM identity, committed rows and capacity. Fault cases include failed
recovery, lost acknowledgements and stale authority. Native KVM/vsock, restart,
private-environment and leak acceptance remain required before activation.
