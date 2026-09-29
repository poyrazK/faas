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

The bidi gRPC framing used for every HTTP exchange does not make an ordinary
response a long-lived stream. Only the trusted streaming decision or the app's
gRPC protocol detaches a successful response from its handshake budget. An
ordinary body remains bounded after headers; expiry aborts its transport so a
partial success body cannot appear complete. A detachable session exposes its
independent ceiling to gRPC, while its context cancellation bounds the handshake.
This prevents gRPC's remote timeout from retaining a detached handshake deadline.

The protected compute response carries its absolute deadline in
`X-Faas-Traffic-Response-Deadline` and its successful long-lived decision in
`X-Faas-Traffic-Response-Session`. These are platform-owned transport metadata:
guest headers and trailers cannot author them, and the public proxy consumes
and removes them. The public proxy takes the earlier of its own deadline and
the compute deadline. An invalid or ambiguous deadline refuses the response
before commitment. Only a successful platform-authored session may detach the
handshake budget; a guest Content-Type cannot enable detachment.

Both HTTP hops install a socket/HTTP2-stream write deadline before committing
an ordinary response and interrupt a blocked write when its context is canceled.
Body-copy cleanup waits for the writer to exit after interrupting both source
reads and destination writes. An expiry before commitment may send the canonical
504 using a bounded 100 ms best-effort error-write allowance; a committed body
is aborted without appending a problem document. Standard Go HTTP/1 and HTTP/2
server writers, including the production wrapper chains, support these controls.

Streaming/upgrade handshake time is bounded until successful response
commitment, after which the established idle/session contract applies. An
expired waiter may leave bounded shared wake work running for other waiters.
Detached jobs and arbitrary unmediated guest sockets are excluded. Trusted
remaining-deadline transport for nested managed service calls is a required
follow-up before the complete deadline guarantee is accepted.

Managed HTTP chains carry `X-Gregale-Request-Deadline`, an opaque HMAC-SHA256
token containing an absolute UTC deadline, issue time, chain ID and receiving
app/account. The MAC key is purpose-derived from the existing shared
`FAAS_SESSION_KEY`; ephemeral development session keys are never used. Public
claims are stripped. The MAC is verified before source-instance lookup so
that lookup is bounded too; the resolved caller app and its authorized
account must match the token before wake. Each authorized hop signs the same or an earlier absolute
deadline for its target. Missing verification material, a different key, an
invalid claim or apparent future issue time refuses propagation. The token's
lifetime is capped by the existing managed dependency timeout ceiling.

Node/Python request-context helpers propagate the opaque carrier only to
managed service hosts. Applications must use these helpers or deliberately
forward their request's carrier; concurrent requests cannot safely be inferred
from a VM's active calls. Omission is an unlinked call, never described as
protected by a parent deadline. Long-lived sessions omit the ordinary chain
carrier. Keys must be shared across participating gateways, clocks must be
synchronized, and coordinated key rotation may refuse old in-flight tokens.
Cross-node clock and key-rollover acceptance remains required.

## Shared traffic accounting

`[ratelimit] mode = "central"` is the daemon default and requires its Postgres
pool. `local` remains an explicit operator/development exception; unknown
values refuse startup. Central app/account/rule consumes are atomic. Store
errors refuse unverified admission with `rate_limit_unavailable`/503 and do
not substitute a private bucket. Exhaustion still returns 429. Account and
app allowances charge eligible authenticated ordinary public requests before
cache lookup, once per logical request. Sequential scope checks are not a
multi-scope transaction: an account debit can precede an app refusal, and
ambiguous store timeouts are not refunded.

The default aggregate retry counter uses the same Postgres pool. Atomic
observations and spends share one ten-second database window per target app,
including the public and declared service-proxy retry loops. Cache responses,
ineligible requests and additional attempts do not inflate the original
denominator. A failed original observation or spend refuses replay while the
original request can run once. Expired counters require a new observation;
replacement processes cannot mint a fresh minimum. Expired rows are pruned in
bounded batches, and app deletion removes them. Existing explicit Redis
credentials still select the Redis backend and its startup/runtime posture
from ADR-288. This supersedes ADR-288's implicit process-local default.

## Node-owned HTTP admission

vmmd derives each routed instance's cap from its schedd-authored wake plan.
The same node gate covers `ForwardHTTPStream` and `ForwardRawStream`, including
public, declared-service and managed synthetic HTTP callers. Missing/unknown
plans or an unwired admission owner refuse forwarding. Jobs, disposable
executions, task VMs and builders do not expose an ordinary routed listener.
TCP service tunnels and background guest work are outside this HTTP cap.

A permit is acquired before bridge creation or invocation hooks and remains
held through the full exchange, including streaming bodies and upgraded
sessions. Client cancellation never frees it by itself. The persistent v2
bridge acknowledges completion on its protected local socket after its guest
handler finishes cleanup. A missing, failed or timed-out acknowledgement
fences and reaps that bridge before releasing the permit. Other exchanges on
the uncertain bridge can fail; preserving verified capacity takes precedence
over keeping that child alive. Per-RPC v2, raw and compatibility bridges reap
their children before release; Linux cancellation also kills the compatibility
shell's process group. Reusable bridge generations use distinct socket paths.

Park, migration preparation, process exit and destruction retire admission
and cancel active forward contexts. Network/lease recycling waits for real
permit releases. Failed migration drains can resume the same VM while keeping
its pending cancelled exchanges counted. Late releases cannot decrement a
replacement's count. The live VM status reports enabled, actual plan/cap,
inflight, generation and retirement state. A plan change takes effect on a
new instance; status reports the plan actually used by a still-live VM.

Existing gateway capacity estimates/queues remain hints before this final
node gate. A racing full node returns `concurrency_throttled`/429 with a short
retry hint; it does not add an unbounded node queue, evict a healthy placement,
or trigger application replay. Unverified node admission returns
`http_admission_unavailable`/503. Both outcomes occur before guest execution.

The managed systemd unit explicitly uses `KillMode=control-group`. Linux
bridge parent death uses SIGKILL so an old bridge cannot keep its former
owner's exchanges alive through replacement. Before serving RPCs, vmmd checks
its private bridge socket directory, removes only confirmed dead sockets and
refuses startup when an old socket still accepts connections. These process
and restart fences require native Linux acceptance and leak evidence.

## Effective request policy

The public routing handler pins the entire compiled host policy before route
substitution. The snapshot includes resolved external CORS presets and named
environment policy, rather than only stored action JSON. A cache refresh does
not change that request's rules during upload, wake, retry or forwarding.
After owner resolution, the resolved app flags and plan bounds join the
snapshot. A versioned digest identifies this effective input without exposing
credentials or raw policy. Store failures and owner-specific compile failures
refuse an unverified snapshot before customer authentication or guest work.
Another account's broken free-form host match cannot block this tenant.
Empty verified policies remain
valid snapshots. Runtime response/span evidence records the digest.

Ordinary policy updates fence new requests during convergence; an admitted
request retains its snapshot. Emergency security revocation uses a separate
durable generation for an account, app or deployment. Account suspension/abuse
hold, app deletion and deployment security quarantine advance that generation;
release advances it again so a missed revoke/release pair cannot revive an old
request. Generations retain deleted identity tombstones and contain no policy
or credentials. Ordinary rule, configuration and deployment-weight updates do
not advance a security generation.

Every participating HTTP admission verifies its security scopes in Postgres and
registers a cancellation fence. Public requests enroll account/app before wake
and the selected deployment before forwarding; managed service calls enroll
caller/target scopes before wake and selected deployments before forwarding.
An independent cancellation root survives successful stream/Upgrade handshake
detachment. Normal completion unregisters the request without prematurely
canceling the HTTP server's final buffered flush. Revocation cancels the actual
exchange; resource permits remain owned until forwarding cleanup completes.

Each gateway re-reads all active scopes at least once a second with a 250 ms
store-operation limit. Notifications request an earlier read; they never grant
or revoke traffic based only on payload claims. A changed generation, an active
revoke, a regressed/inconsistent store result or failed verification cancels the
affected exchange. Store outages refuse new admissions and cancel active
tracked traffic; no private allow fallback or indefinite warm lease is used.
Tracking is bounded by 65,536 exchanges and 4,096 distinct active scopes per
gateway; exhaustion refuses admission. The limits live in pkg/api/limits.go.
This fence initially covers account/app/deployment security state, not individual
credential revocation, managed realtime, detached work or arbitrary guest sockets.
Preview agreement, declared internal-path coverage and update/recovery evidence
remain required delivery work.

## Delivery and verification

The implementation tracker is `docs/traffic_platform_implementation.md`.
Each guarantee needs configuration-to-runtime tests, cancellation/recovery
tests and customer documentation. VM lifecycle changes require native x86_64
Linux KVM metal and leak checks before acceptance. Fleet limits need real
multi-process/store tests. Code and local unit tests alone do not establish
deployed availability or control-plane HA.
