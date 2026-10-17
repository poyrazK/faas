# ADR-1344: Qualify disposable object nodes across SSH hosts

- Status: Accepted for development qualification; independent-host evidence pending.
- Date: 2026-10-17
- Related: [ADR-1341](1341-owned-object-storage-local-qualification.md), [ADR-1343](1343-owned-object-storage-publication-preparation.md)

## Context

Publication preparation improved foreground read latency in local matched runs,
while the new replication budgets reduced replica-mode throughput. Those runs
share a host and filesystem. The current qualification runner directly invokes
offline filesystem APIs, so pointing its daemon at another endpoint would leave
bootstrap, maintenance and promotion on the wrong disk.

We need repeatable traffic, failures and work-budget comparisons with primary
and follower storage on separate machines, while preserving their loopback-only
listeners and the existing publication and acknowledgement contracts.

## Decision

Extend `object-storage-qualify` with an optional OpenSSH follower. Run the
controller and original primary on the first Linux host. Run a qualification
agent, node and catch-up worker on a second Linux host. Preinstall matching
daemon/control binaries and the qualification executable; use standard OpenSSH
configuration, known hosts and noninteractive authentication. The harness does
not provision hosts, install software, disable host-key verification or accept
existing node data.

The agent speaks bounded, sequential JSON on the SSH session's private stdin and
stdout. Generated node credentials travel in that stream and child environments,
not command arguments or reports. Its operations are fixed node lifecycle,
bootstrap, status, compaction, backup transfer and planned authority transitions;
there is no generic command execution operation or production fault route.
Each durability mode exclusively creates a new remote private directory using
the supplied prefix plus `-local` or `-replica`. EOF, cancellation and protocol
failure kill and reap managed children, retaining logs and disposable data.
EOF immediately cancels an operation already in progress. Handle SSH hangup and
broken-pipe signals so cleanup can run. Linux parent-death signals terminate
children if the controller or agent dies abruptly; keep each creating OS thread
alive through process reaping, including the managed SSH master.

Use an OpenSSH control socket in a private temporary directory. A reverse
loopback forward lets the remote follower reach the controller's existing
replication proxy. Local forwards let the controller reach the remote daemon and
metrics after promotion. All service listeners remain loopback-only. The measured
replication route includes the controller and SSH encryption; it is not a direct
host-to-host or native WAN benchmark. SSH configuration must permit the necessary
local and remote forwards.

### Remote state and failure boundaries

Bootstrap transfers a verified offline seed, including empty directories, in
bounded 64 KiB file chunks. New
destination files are exclusive and private. Root-confined paths, regular-file
and single-link checks reject escapes, symlinks, aliases, replacement and
out-of-order chunks. Bound total transfer size and entries by the disposable
qualification and existing backup budgets. Verify imported backups using existing
node code before bootstrap. Incomplete imports grant no serving authority.

Compaction and maintenance execute on each node's own host while stopped. Use the
existing fence, catch-up and promotion APIs on the remote follower. After
promotion, export and verify its offline backup, bootstrap a new standby on the
original controller host, register/configure it on the promoted node, then restart
and verify successor writes and historical proofs. No new storage format,
automatic failover, public listener or SQL is introduced.

Report the remote executable digests, platform, filesystem type and a SHA256 of
each kernel boot identity. Require matching local/remote daemon and control
digests. By default reject equal kernel boot identities. An explicit
`--allow-shared-host` supports local transport validation and records
`distinct_kernel_boots: false`. Different boot identities indicate separate
kernel instances, not independently verified physical storage failure domains.
Always report physical failure domains as unverified; operators must document
the actual storage layout separately.

### Network profiles and operating measurements

Expose follower byte-work and entry-start rates in the runner and pass them to
continuous workers, restored followers and promotion catch-up. Keep ADR-1343's
defaults and maxima. Add a bounded delay per replication RPC and an optional
aggregate response-body byte rate for replication payloads at the proxy. Both
are cancelable and apply only to replication traffic. Payload shaping has one
64 KiB burst; it conservatively reserves requested read bytes, including EOF
probes. It models application transport pressure, not packet loss, kernel TCP
behavior or a precisely measured wire rate. Existing partition injection rejects
replication RPCs, including retries after a change wait wakes or times out.

Version 2 reports retain operation/error counts, quantiles, workload ceilings,
named correctness checks and resource coverage. Add network/work configuration,
per-mode topology, successful remote sample counts, and rejoin observations after
partition healing and primary/follower restarts. Rejoin time begins at healing or
restart and ends at the normal applied/confirmed catch-up check. Check durations
still include the broader failure scenario. Sample remote RSS and physical files
on the remote host and aggregate them with controller measurements; do not read
remote PIDs or paths through local procfs. Peaks remain sampled lower bounds.

Run each budget/profile with fresh paths and identical workload parameters.
Compare actual workflows, admission stop reasons, operation attempts, p95/p99,
throughput, sampled lag and rejoin times. Preserve failures and adverse tradeoffs.
No hardware-independent latency target or recommended production setting is
inferred from a short shared-host fixture run.

## Validation and consequences

Require tests for remote option bounds, backup escape/alias/replacement refusal,
agent disconnect cleanup, cancelable shaping and unaffected customer traffic.
Exercise the complete lifecycle using actual OpenSSH and built node binaries in
both durability modes, including remote promotion and the standby host reversal.
Retain the local regression path. The opt-in SSH integration test accepts
configured disposable targets; shared-host validation must explicitly permit and
report its shared kernel.

Actual independent-host capacity and recovery limits require reachable disposable
hosts, their storage layout and repeated runs there. PostgreSQL ledger recovery,
OS resource isolation, drive faults, billing authority and production enrollment
remain outside this fixture.
