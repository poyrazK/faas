# ADR-375 · Complete traffic enforcement guarantees

- **Status:** accepted for implementation; acceptance evidence pending
- **Date:** 2026-09-29
- **Decision:** Finish ADR-201's outbound connection enforcement, add a total
  ordinary-HTTP request deadline, make advertised fleet rate/retry budgets
  authoritative, and enforce per-instance admission at the compute-node
  forwarding boundary. Pin effective policy for each request and expose the
  covered traffic path and actual enforcement state.
- **Why:** A configured control must reach the path it protects. Current egress
  updates skip disabled VM configurations; the app request budget starts after
  admission and wake; active concurrency counters and default rate counters
  are gateway-local. Existing convergence status and policy preview are useful
  foundations but do not prove those runtime guarantees.
- **Consequences:** Customer configuration, transport metadata, failure policy,
  tests and capability evidence must be delivered with each mechanism. Keep
  existing execution-timeout semantics explicit during migration to total
  deadlines. Shared wake work may outlive an expired waiter but remains bounded.
  Streaming switches to its idle/session contract after response commitment.
  Node admission counts forwarded exchanges, not background work surviving a
  disconnected request. A permit cannot expire while its old bridge can still
  forward. Mandatory global protection must not silently degrade to local
  counters. Operator disable switches remain explicit, observable states.

## Outbound connection protection

vmmd owns startup enablement and all namespace mutations. schedd owns probe
state and desired circuits. Both address families must declare their sets and
reject rules. Updates are whole-set atomic nft transactions. New and restored
VMs receive current desired state before guest execution; pending networks
participate in live updates. A parked app retains desired state, and periodic
reconciliation repairs missed updates and clears retired/disabled upstreams.
Enforcement must not acknowledge success when the feature is unavailable.

schedd commits normalized whole-app targets with a monotonically increasing
revision in `app_egress_circuits` before fanout. Enabled vmmd nodes perform a
read of that schedd-owned policy before network readiness, including new
placement and process restart. This adds a bounded database dependency to new
VM boot (not the request picker): source failure refuses that boot. Delayed
revision pushes reapply the newer remembered policy. vmmd never writes the
desired table. Opt-out removals and unchanged open policies are retried each
tick; a stored empty policy remains a reconciliation subject after restart.
Recent actual probe timestamps are replayed, including per-upstream customer
thresholds, without counting the same probe twice. Evidence older than four
probe intervals releases rejection when schedd can read the feed; a store
outage retains the last committed policy. DNS errors retain known targets and
surface failed reconciliation. The supported set is current A/AAAA answers,
up to 64 per upstream / 3200 per app; no truncation is silently acknowledged.
Targets sharing an address and port share connection rejection.

`Stats.instances.egress_circuit_enforcement` reports enabled, desired/applied
revision, successful completion and target count from the compute node. The
existing scheduler gauge describes logical health only. Established sockets,
application errors and arbitrary addresses cached outside the refreshed DNS
answer set remain outside this connection primitive's coverage.

This is protection of new TCP connections to resolved addresses and ports.
Established connections are accepted. DNS sets must include supported A/AAAA
answers and be bounded and refreshed. Application-aware HTTP dependency
breaking over existing connections requires a separate managed proxy path.

## Total ordinary-HTTP deadline

The existing budget action gains optional `total_deadline_ms`. The required
`budget_ms`, app `request_timeout_s`, and their override header retain their
execution semantics (after upload/wake/admission). The total deadline is
separate and cannot be increased by that execution override. It is measured
from the trusted public proxy's receipt time, before upload. The protected
public-to-compute transport replaces client timestamp claims; the compute
listener consumes the platform timestamp and removes it before guest forwarding.
Rule lookup uses the public route at owner resolution. A configured total
rule is pinned for the later execution budget, so rewrite/wake cannot change
its deadline. Deadline expiry during upload returns the same 504 problem as
admission or forwarding expiry. Both timers are released on every exit.

Streaming/upgrade handshake time is bounded until successful response
commitment, after which the established idle/session contract applies. An
expired waiter may leave bounded shared wake work running for other waiters.
Detached jobs and arbitrary unmediated guest sockets are excluded. Trusted
remaining-deadline transport for nested managed service calls is a required
follow-up before the complete deadline guarantee is accepted.

## Delivery and verification

The implementation tracker is `docs/traffic_platform_implementation.md`.
Each guarantee needs configuration-to-runtime tests, cancellation/recovery
tests and customer documentation. VM lifecycle changes require native x86_64
Linux KVM metal and leak checks before acceptance. Fleet limits need real
multi-process/store tests. Code and local unit tests alone do not establish
deployed availability or control-plane HA.
