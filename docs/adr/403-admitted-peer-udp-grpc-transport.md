# ADR-403: Admitted-peer UDP transport through VMMD

## Status

Proposed; native Linux/amd64 qualification pending.

## Context

Stateless container workloads can declare UDP ports. The public gateway owns
public sockets and validates listener/account ownership. VMMD alone may enter a
live instance's namespace. A byte stream must retain datagram boundaries,
including empty payloads, and bound resource use independently in both directions.

## Decision

Add ForwardUDPStream to the existing VMMD service. The first request supplies a
live instance and guest port 1..65535. Each subsequent oneof datagram frame
contains exactly one payload; an empty payload retains its presence on the wire.
Receiver defaults are 64 MiB and 65,536 datagrams per direction; caller caps can
only tighten them. Payloads over 65,507 bytes and repeated initialization fail
before forwarding. Request EOF ends the peer session because UDP has no half-close.

Reuse VMMD's namespace helper launch with ADR-402 bounded readiness. Activity
tracking prevents parking during an active peer. VMMD closes pipes, cancels,
kills and waits for its helper when the handler returns. Both frame directions run concurrently and either can terminate the handler,
including a request failure while reply Send is blocked by HTTP/2 flow control.
The Send/Recv goroutines use the gRPC stream context, whose cancellation is
controlled by gRPC when the RPC ends; waiting for them before returning would
deadlock that shutdown path.
Preserve initial transport cancellation/failure status and record the actual
terminal error in VMMD operation metrics.

The gateway forwarder requires cancellation-aware peer Send/Receive, retains
ownership of the public shared socket, resolves a compute node, validates bridge
readiness, checks each direction's budgets, and cancels/joins its sender on exit.
Idle activity in either direction resets the 30-second default idle timeout.
Include the UDP helper in release and Packer build inventories and declare its
absolute-path override in the environment contract.

## Consequences

This is transport for an already admitted peer, not public UDP admission itself.
Public listener ownership, source-CIDR filtering, bounded queues, account/session
and rate controls remain mandatory before exposing it. No persistent guest disk,
host directory sharing or ARM64 support is added. Portable protobuf, peer,
readiness and real gRPC rejection/deadline/flow-control shutdown tests do not establish native namespace,
VM lifecycle, node-loss or leak acceptance. Those require the designated native
Linux/amd64 KVM environment before this service is considered qualified.
