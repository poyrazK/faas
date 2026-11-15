# ADR-20261113210000 · Total serving-probe attempt deadlines

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-13
- **Extends:** [serving-probe diagnostics](20261113190000-serving-probe-failure-diagnostics.md).

A socket timeout limits inactivity rather than the complete request. A resolver
can also block outside socket timeout handling. Slow-streaming responses can
therefore delay the remaining URLs and prevent result publication before the
service's outer timeout.

Run each attempt in a disposable forked worker in the existing single-threaded
Linux helper. Start one monotonic deadline before worker startup, covering DNS,
connection, TLS, headers, body validation and response cleanup. The parent waits
only for the remaining budget and rejects results received after the deadline.
Kill and reap a worker that is still alive when the attempt finishes, including
timeouts, failed IPC and parent cancellation. Close pipe and process descriptors
on every path. Keep at most one worker alive under the existing service limits.
Process cleanup and scheduling can add overhead beyond the configured budget.

Publish observed HTTP status and first-byte timing to the parent before blocking
reads so a body timeout retains those observations. Before the first byte, use
elapsed time to failure. Report deadline expiry with existing timeout code 4;
an unexpectedly exited worker uses code 8, with fixed safe text. Do not emit
exception text, raw URLs or response bodies through worker messages.

Preserve two attempts, retry delay, sequential URL processing, atomic metrics,
notification cadence and heartbeat eligibility. Keep the outer systemd timeout
as the complete-run guard. Use real local I/O tests for stalled headers and
first bytes, a body that sends bytes within the socket timeout, blocked DNS,
late responses, worker cleanup, cancellation and continuation to later URLs.

See the [operator guide](../ops/serving-probe.md).
