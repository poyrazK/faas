# ADR-163 · Framework readiness over the Firecracker stream bridge

- Status: Proposed
- Date: 2026-09-06
- Acceptance: framework-ready publication, snapshot replay, and zero-residency gates (§14).

## Context

A fresh Node22 deployment served requests but never qualified for warm snapshot
promotion. In addition to the separate dropped activity-count delta, VMMD could
not bind its host AF_VSOCK datagram receiver. Firecracker 1.7 bridges guest vsock
to per-VM Unix sockets; its host interface is not the host kernel's vsock bind.
See the [upstream protocol](https://github.com/firecracker-microvm/firecracker/blob/v1.7.0/docs/vsock.md).

## Decision

Keep the runner's existing local Unix-socket ready message. The socket is owned
by root with the configured workload's group and mode 0660; the workload runs
as its existing unprivileged UID. Guest-init retains
the first valid message in guest memory and exposes a bounded, versioned status
reply on guest vsock STREAM port 1027. A pending reply cannot promote a snapshot.
A warm snapshot retains the ready state, so restoring it does not require the
runner's captured sync.Once to fire again or execute customer code again.

VMMD reads the reply through that instance's Firecracker Unix bridge using
CONNECT 1027. Identity is derived from the host-owned instance/socket mapping;
the guest supplies no instance ID. One daemon-lifetime observer per live VM
retries transient transport or publication failures. Reads and RPCs have finite
deadlines; park, destroy, migration pause, and shutdown cancel the observer.
No readiness polling or scheduler writes are added to the customer response path.

Firecracker resets vsock connections around snapshot restore. Metal tests with
recent readiness traffic exposed an accepted resume connection closing before its
ACK. Retry the complete resume exchange at most three times for transport EOF,
reset or broken pipe, within one five-second deadline. Each attempt generates
fresh host entropy. Negative ACKs remain terminal, and readiness still requires
ACK=0. See [snapshot transport reset](https://github.com/firecracker-microvm/firecracker/blob/v1.7.0/docs/snapshotting/snapshot-support.md#vsock-device-limitation).

VMMD reports the receipt to the owning scheduler through ReportFrameworkReady.
The scheduler authorizes instance ownership, serializes with lifecycle changes,
and stamps its own clock on a running instance. Replays preserve the first stamp
and cannot move the warm-promotion age floor. Existing plan, opt-in, request-count,
and age gates remain required. VMMD does not write the readiness column directly.

## Rollout and limits

Deploy the scheduler RPC before VMMD and the new guest-init image. Existing
snapshots retain their old guest-init; create a new deployment/capture to validate
the stream path. Unsupported guest endpoints leave promotion pending; they do not
make an unready runtime eligible. Other legacy datagram event channels are a
separate migration and are not made reliable by this change.
