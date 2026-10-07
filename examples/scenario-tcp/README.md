# TCP dependency resilience

This suite runs a Node application and a private TCP echo dependency in isolated
Gregale developer VMs. It checks all four TCP faults through the application's
real socket and runs a staged baseline, latency, and recovery sequence over one
persistent dependency connection.

Requirements: Node 22+, a logged-in Gregale CLI, an account with capacity for two
developer workloads, and compute nodes with private service TCP routing enabled.
The suite defaults to warm VMs; use `--profile all` for warm, cold and restored
runs and increase the workload-minute budget to 160. Timing assertions assume
an otherwise responsive test environment.

```sh
cd examples/scenario-tcp
gregale test --suite resilience --validate
gregale test --suite resilience --preflight
gregale test --suite resilience --max-workload-minutes 60 \
  --report results.json --html results.html --junit results.xml
```

Each scenario installs an eight-second, 100-percent fault: 250 ms downstream
latency, 16 KiB/s downstream bandwidth, stalled downstream bytes until the
application's two-second deadline, or an immediate connection reset. The
assertion command verifies the observed outcome and recovery. The runner cleans
up both workloads and their test namespace, including after failed assertions.
JSON and HTML reports include installed rules and expiry. Warm TCP lifecycle
evidence records absence of a new wake; socket behavior is asserted by the app.

The `tcp-staged-recovery` scenario measures the `/pool/probe` endpoint in each
stage. It records p95 and p99 latency and requires five samples, with separate
budgets for baseline, 250 ms downstream latency, and recovery. It clears the
fault and polls recovery for up to ten seconds while relative SLO gates are
evaluated. The `min_matches: 1` rule also fails the fault step if the probe never
traverses the dependency proxy. Per-stage SLO measurements, fault matches, and clear
operations appear in terminal, JSON, and HTML reports. Recovery compares its
p95 latency and success rate with baseline and reports the attempt count and
elapsed recovery time.

To test an external TCP dependency, replace `tcp-echo` with `tcp-relay`, choose a
private listener such as `15432`, and set `upstream: test-db.example.com:5432`.
Configure authorized test credentials separately. Keep TLS verification enabled
with the original upstream hostname as the TLS server name. See the
[scenario guide](../../docs/scenario-tests.md#external-dependencies-through-a-relay)
for egress policy and fleet prerequisites.
