# ADR 401: Preserve datagrams across guest socket helper pipes

Status: proposed

Use a four-byte big-endian length followed by one complete UDP payload. Empty records are real datagrams. Reject oversized headers before payload allocation and truncated headers/payloads before forwarding. The maximum IPv4 guest payload is 65507 bytes, declared in the shared limits table. Writes handle short progress and reject zero-progress writers.

The bridge owns its connected UDP socket and closeable pipe endpoints. EOF, transport failure, or cancellation ends both directions and joins both forwarding goroutines. A context already canceled on entry closes resources without reading queued input. Closing the output pipe releases blocked reply writes. This protocol supplies no delivery guarantee or UDP half-close.

The helper receives guest IPv4 address and port from VMMD. Descriptor 3 must be a writable pipe before the UDP socket is opened, preventing a missing/mistyped readiness descriptor from aliasing another file or the guest socket. Readiness means socket setup only; it does not prove guest application readiness. Namespace entry, privileged launch, budgets, gRPC integration, packaging, and native acceptance remain subsequent work.

Validation uses race-enabled framing and real UDP socket/pipe tests plus a child process executing the actual helper entrypoint. Cases cover empty/binary records, bounded size, malformed input, short writes, pre-cancellation, blocked I/O cancellation, descriptor validation, readiness, replies, and EOF exit. Darwin socket round trips use smaller host-supported messages; Linux tests retain the full-size socket case. Linux/amd64 compilation is distinct from native guest namespace execution and leak acceptance.
