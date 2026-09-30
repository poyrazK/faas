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

Before public HTTP cache lookup or dispatch, release resolution, revision-pin
eligibility, host-pinned deployment availability and scoped positive weights
join one bounded read-only repeatable-read view. Owner identity is verified in
that view. Selection uses exact host/smoke pins, then release/revision pins,
followed by the effective version key or local weighted entropy. The selected deployment is
held through wake, capacity waiting and retries. Session affinity and instance
rotation operate only within that deployment. A cold cohort is woken exactly;
there is no warm sibling fallback. Cache partitions follow the selected
deployment. Routing verdicts and weights join the request fingerprint; entropy
and the individual weighted choice remain separate decision evidence. Pure
edge responses do not acquire unused dispatch policy. Async queueing retains
its separate dispatch owner and still needs complete synthetic-path evidence.

Exact public wakes retain one local app queue across cohorts, one total wait
allowance and the gateway-wide admission queue. A different cohort waits for
the current generation, then retries only its own deployment. Browser wake
pages retain bounded detached work. Ordinary burst expansion keeps the app's
single worker and sends the admitted deployment on the first scheduler call
and every bounded continuation. It retains steady app/plan ceilings and
readiness filtering. A cold second positive cohort beside a routable cohort
may use the existing bounded rollout overlap; explicit rollout verification
retains its existing scheduler-controlled allowance. Detached cache refresh
uses the same admitted deployment. Cross-process exact-wake coalescing and atomic host/alias/environment
binding with the earlier resolved app settings still require follow-up.

Managed service discovery, alias access, binding/target authorization,
preview/test namespace, method/path grant, reliability and transport posture
are read from one bounded read-only repeatable-read transaction. The transaction
ends before queueing, wake or dispatch. The gateway copies the result and pins
its fingerprint through wake and retries; the configured call timeout counts
from service-handler entry. New calls require a fresh verified view. The
projection and evidence exclude unrelated manifest fields and credentials.
Release membership and expiry, exact override eligibility and positive deployment
weights use that same committed view. A routing roster is bounded to 100 positive
deployments; exceeding it refuses verification. One deployment is selected before
wake: release membership, then exact override, then affinity or weighted selection.
Ordinary calls without an affinity key use local random selection against those
weights. Instance rotation and local-node preference operate within that selected
deployment. Cold wake and retries cannot switch deployments when weights or release
graphs change. A request without an eligible deployment refuses before wake.
Selection entropy and the individual random choice do not change the policy
fingerprint; the selected deployment is separate span evidence. Live endpoint
health and independent security generations can still refuse or cancel dispatch.
These routing checks have local fixtures and Postgres evidence; deployed path,
load and native lifecycle acceptance remain required.

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
Tracking is bounded by 65,536 registry registrations and 4,096 distinct active
scopes per gateway, and 16 distinct scopes per request across all attempts;
exhaustion refuses admission. Account/app and later deployment enrollment may
use separate registrations, so that bound is conservative for logical requests.
The limits live in pkg/api/limits.go.
The compute response transfers its exact admitted scope generations in the
protected `X-Faas-Traffic-Security` header. The public hop verifies equality
against Postgres before committing the response and independently enrolls the
exchange. A fresh generation cannot replace the compute baseline: even a missed
revoke/release pair refuses the old exchange. Public cancellation interrupts
blocked HTTP writes and closes both sides of a hijacked upgrade. Registrations
remain until copying and transport cleanup finish. Guest headers, actions and
trailers cannot author this metadata; the public hop consumes it. Missing or
invalid metadata refuses a successful application response. Pre-admission
errors may have no scopes, and the separate managed realtime owner explicitly
marks its excluded surface. Compute must be upgraded before public enforcement.
This fence initially covers account/app/deployment security state, not individual
credential revocation, managed realtime, detached work or arbitrary guest sockets.
Preview agreement, declared internal-path coverage and update/recovery evidence
remain required delivery work.

## Delivery and verification

Public host resolution reads hostname ownership, app settings, account plan,
environment/release membership and deployment ingress through a narrow,
read-only repeatable-read view. New requests require this view even if the
route cache is warm or notifications are lost. An unavailable view refuses
admission. Ordinary project app hosts use production deployments; standalone
hosts use the default scope. Revision and alias hosts project ingress from
their exact deployment, including a retained zero-weight alias revision.
Other environments cannot alter the primary ingress or companion roster.

The host projection has a private content fingerprint. The later public
routing transaction re-resolves that projection before reading eligibility
and weights. If settings or hostname bindings changed between the two reads,
the request refuses dispatch instead of combining generations. Both
transactions end before wake; an admitted request retains its complete
verified projection and selected deployment during wake and retries.
Compiled edge-policy agreement and complete path evidence remain required
before the policy guarantee is accepted.

Public edge policy resolution has two phases. The initial readonly transaction
resolves hostname ownership and reads that owner's bounded route-only graph.
Global route lookup is limited to genuinely unclaimed, substitutable hosts;
reserved misses and immutable deployment URLs skip that unused graph. The
matcher uses the same namespace configuration as the public router and refuses
production resolution if that configuration is unavailable. A private root
claim baseline survives owner compilation and is rechecked in its transaction
before an edge response or dispatch. Changed ownership, namespace configuration
or root metadata refuses reuse; a fresh request can resolve the new claim.
The graph selects the owner/target without loading unrelated tenants' presets.
The hostname/app transaction then reads the verified owner's complete rule
set, referenced CORS presets and the stable environment URL's overlay. It verifies the
earlier route graph, compiles the owned inputs, and supplies sealed host
policies before security checks. Compiler cache reuse requires a matching
content baseline; notifications are an optimization. Unknown synthetic hosts
verify the global route graph; claimed hosts verify their owner's routes.
Source and target baselines must agree before a substitution can return an
edge response. Production target loaders retain read failures and refuse a
selected route whose target is missing or unavailable; they cannot fall back
to an ordinary source-app response. Dispatch rechecks all these inputs in the routing view, while
admitted wake/retry work retains the sealed policies. Reads are bounded to
50,020 matching rules and 64 MiB of canonical projection per host, with
512 KiB per preset or environment overlay. Referenced presets count toward
the aggregate projection bound. The compiled cache retains its existing host-count bound.
Complete path, overload and preview/runtime evidence remain required.

Individual preset, environment-overlay and scoped-route replacements are
validated against their complete runtime projection before persistence. The
Postgres store measures canonical JSONB bytes, including ownership metadata
and JSONB whitespace; the in-memory store uses a conservative serialization
bound. Failed writes retain the previous policy and return a stable 422 problem
with byte limit, observed size and recovery guidance. A smaller replacement or
an empty overlay remains available for repairing an existing oversized row.
This individual-object guard is paired with the ordinary owned host aggregate
check below. Combined scoped/global validation and its in-memory mirror remain
delivery work; an account-wide byte quota must not silently replace the
per-host runtime bound.

Edge-rule creates and updates also validate the complete canonical saved row
against the host projection's 64 MiB ceiling before committing. The guard
includes metadata and action fields outside the active union member, because
the runtime SQL transfers those fields too. Rejection rolls back intent and
its transactional change event; deletion and smaller replacement remain repair
paths. The in-memory store uses a conservative serialized bound. Rule creates,
rule updates, preset creates/updates, environment edge overlays and environment
clones acquire the same account row lock
before any app or policy-row lock, then retain it through validation and
commit. Contenders use NOWAIT and roll back before a context-bounded retry,
so they do not reserve pool connections while waiting. This is the
serialization boundary for aggregate validation, not an
account-wide byte quota. The ordinary owned host check uses this boundary.

Owned host aggregates are validated in the same account-locked transaction as
the mutation. The store compares skinny before/after projections of enabled
rule groups and referenced presets, without transferring their action bodies.
It uses the SQL selector language (exact selector or its PostgreSQL LIKE
translation), including existing percent/underscore/escape semantics. A
bounded automaton explores shared host languages and deduplicates referenced
presets at each accepted hostname. A flat account byte quota cannot replace
this check. Canonical SQL bytes/counts and a conservative bound for the Go
compiler's escaped JSON are checked separately. The estimate fills mandatory
decoded Go fields and the preset FK mirror, retains SQL whitespace and
reserves timestamp/escape expansion. Unsupported legacy case aliases refuse
after-analysis until their rule is replaced, disabled or deleted; an
undercounted decoded shape cannot authorize a write. A legacy oversized host
may be repaired incrementally when analysis finishes and none of its over-limit
dimensions increases. New overload and increased legacy overload refuse
before commit, rolling back
intent and change events. Metadata, automaton nodes/states/bytes, transitions
and analysis time have explicit resource ceilings. A server-side SQL timeout
precedes the phase context timeout and the prior setting is restored on
success, so a blocked query releases transaction locks before its refusal.
Exhausted analysis refuses the mutation with a distinct error rather than
accepting unverified policy.
Named environment aggregates distinguish the account-owned canonical read
from the app-owned compiled result. The first retains every matching rule
because the gateway bounds that read before applying the environment filter.
The compiler then keeps only the named app, replacing its headers/CORS when
an overlay row exists, including an explicit empty row. Overlay rules receive
the same synthetic identity/default fields as runtime. Compiled counts/bytes
and distinct presets are checked independently from the original read; an
unused sibling app or replaced preset cannot consume compiler capacity.
Registered environment/app identities join the bounded management projection,
and exact host markers split the selector automaton at those URLs. Runtime
and management share the pure environment-host encoder/decoder and deployment
suffix contract. Overlay payloads remain in SQL; only scalar measurements and
identity metadata cross the management store boundary. Overlay writes, new
environment registration and clones use the same before/after account
transaction as rule/preset changes. A newly registered URL starts with no
serving-policy baseline; an old oversized wildcard cannot authorize exposing
a new oversized scope. App membership/reactivation writers, global synthetic
route discovery and the in-memory aggregate mirror still require their own
integration and evidence.

Imported documents also validate canonical runtime bytes before replacement;
the existing import-body cap alone cannot bound JSONB numeric expansion.
Replacing an owned import reuses its account quota slot, including repair of
an oversized legacy document; unavailable plan tiers still refuse imports.
The quota decision remains serialized by the account lock. Environment cloning
validates copied target projections in its transaction before commit, including
app-wide fallback routes and the target slug's bytes. Failure rolls back the
entire target configuration. The in-memory clone preflights all projections
under its existing mutex before creating target state. Global aggregate
validation, its in-memory mirror, app membership/reactivation integration and
complete-path recovery acceptance remain pending.

Public declared-route inputs join the fresh hostname/app view. An exact
environment's route overlay is read before deciding whether an imported
OpenAPI document is needed; explicit routes keep precedence. The readonly
projection includes an authoritative missing document and is owner scoped.
Canonical contract payloads are bounded to 512 KiB before transfer, allowing
the existing 256 KiB import limit plus JSONB formatting expansion. The private
document carrier is copied before app sealing; raw bytes are excluded from
serialized app and request evidence. Matching and observation compile that carrier, rather than loading
another cache/store generation. The later dispatch transaction rechecks its
content with the hostname baseline before eligibility and weights. A contract
change before dispatch refuses; admitted wake/retry requests retain their
contract. Compilation cache hits require the same document content digest.

An edge route substitution retains both the source hostname claim and the
target app projection. An authoritative unclaimed hostname is a verified
negative claim, not a missing proof. An unroutable app, unverified domain,
tenant reservation, deleted app tombstone or failed alias is still reserved;
a rule cannot turn that refusal into a synthetic host. Immutable deployment,
alias and named-environment URLs retain their exact dispatch and cannot be
substituted. Dispatch rechecks both content baselines
in the same read-only transaction as target eligibility and weights. Each
projection has an independent fingerprint recorder over that transaction.
Changed source ownership, a new claim on a synthetic hostname, a disappeared
claim or changed source settings refuse dispatch. A claimed source and target
must belong to the rule's account. Missing source proof cannot authorize a
production substitution. The sealed effective fingerprint includes both
projections; ordinary changes after admission do not alter them during wake
or retries. A claimed source app also joins the account/target app security
fence before wake, so source deletion cancels an admitted routed exchange.

The implementation tracker is `docs/traffic_platform_implementation.md`.
Each guarantee needs configuration-to-runtime tests, cancellation/recovery
tests and customer documentation. VM lifecycle changes require native x86_64
Linux KVM metal and leak checks before acceptance. Fleet limits need real
multi-process/store tests. Code and local unit tests alone do not establish
deployed availability or control-plane HA.
