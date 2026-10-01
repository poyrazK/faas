# ADR-406: Bounded UDP peer and account admission primitives

## Status

Proposed; public socket rollout and native qualification remain pending.

## Context

Public UDP sockets must not allocate unbounded session queues or reset account
budgets when clients change addresses or listener ports. Queued payloads must
retain their original peer identity and must not be shared with a reused socket
read buffer. Cancellation is part of the admission/forwarding contract.

## Decision

Give each admitted peer a four-datagram inbound queue. Copy retained requests and
replies; preserve empty datagrams and reject guest-oversized payloads. Drop newest
input when full rather than block the listener. Replies carry the peer context
and client address; socket writers must reject canceled queued replies. Closing
the peer cancels blocked Send/Receive. Reject already-canceled parents before
constructing a peer, and already-canceled caller contexts before consuming or
publishing queue data. Concurrent cancellation after a call begins is handled by
its context-aware select; no strict ordering against simultaneous queue I/O is
claimed.

Share one admission pool across listener sockets: defaults are 64 peers total
and 16 per account. Acquire before allocating a peer queue. Releases are
idempotent, including concurrent duplicate calls and calls after later admission
generations reuse the released capacity.

Share separate inbound/outbound account packet and payload-byte token buckets
across all listener sockets. Defaults are 1,000 packets/second with 200 packet
burst, 4 MiB/second and a four-maximum-datagram byte burst. Empty payloads consume
packet credit. Attempts consume packet credit even when byte credit is exhausted.
Keep at most 4,096 account entries; evict only entries idle for two minutes and
fail closed when every cached account is active. Peer churn cannot reset bursts.
All defaults reside in pkg/api/limits.go.

## Consequences

These are admission primitives, not socket/VM acceptance. The production socket
owner must share pools and rate ledgers, validate source CIDRs before admission,
retain listener identity, bound its reply queue, honor reply contexts and release
slots on every exit. Public UDP remains disabled by default. Container storage
remains stateless and Linux/amd64 is the only target. Real socket integration,
load, native VM lifecycle and leak qualification remain separate requirements.
