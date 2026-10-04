# ADR-570 · Complete traffic enforcement guarantees

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

Configured compute gateway integration connects a stored public total rule and
declared managed binding through Postgres policy/source identity, separate gateway
processes, the production vmmd admission server and reusable bridges. A caller
fixture spends 300 ms before propagating its opaque carrier using an independent
outgoing context. The managed hop retains the same or earlier authenticated
deadline despite longer execution/binding allowances. The child times out and
both node permits are released before fresh work succeeds. Public carrier claims
are replaced before guest delivery. The test uses the production operator-key
loader; it does not establish SDK propagation, public edge startup, cross-host
clock/key rotation, native VM/network or deployed acceptance. Warm placement,
guest serving and namespace/VM startup remain fixtures. Before readiness, the
warm placement fixture loads the real registry and verifies node, instance,
deployment and port; this primes deployment weights outside the timed request.

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

Connection acquisition failures open the bounded central consult breaker,
which refuses unverified admission without repeated pool waits. Errors
executing the atomic statement on an acquired connection refuse that request
but do not open the breaker: the next request must be able to verify counter
table or row-lock recovery immediately. Neither path admits a local token or
resets shared debt. Backends that cannot classify failures retain bounded
consult backoff; caller cancellation does not open it.

ADR-104 consult coalescing preserves these rules for queued batches. Batch
consumes and initial counter creation use SQLC, retain fractional refill and
serialize replicas on the counter row. Negative elapsed time contributes no
refill, and a future refill timestamp cannot be moved backward to forgive debt.
The PostgreSQL batch recovery and clock rollback regressions are required by
the traffic software CI receipt; coalesced failure recovery also runs under race.

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

Local process qualification also covers the production public routing handler
and managed service proxy through their real forwarders, vmmd gRPC admission
owner and reusable bridge. Mixed exchanges before and after response headers
share the trusted cap across two gateway processes and replacement. Forged
customer cap/plan/instance headers cannot raise it. Structured full/untrusted
refusals do not execute the guest, evict placement or produce an extra observed
forwarding RPC; cancellation cleanup permits subsequent work. App policy,
caller identity, authorization, placement, namespace and VM startup remain
fixtures in this test. Configured daemon, native lifecycle and deployed
acceptance retain their separate requirements.

A further integration case connects configured compute gateway processes to
Postgres routing, declared caller/binding and reliability policy and a separate
vmmd process using the production admission owner and reusable bridge. Two
gateways and a replacement share the cap for mixed public and managed exchanges,
including response bodies held open after headers. Full/untrusted HTTP and
Upgrade refusals preserve the node's structured error without extra observed
RPCs or guest execution; a forged declared caller never reaches forwarding.
Client cancellation is followed through node cleanup and subsequent successful
work. Warm placement, listener/source translation, VM startup, namespace and
guest serving are fixtures. Outer discovery, the separate public edge daemon,
cross-host authentication and native/deployed acceptance are not established by
this case.

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

Selected managed deployment wakes use that same local app gate and gateway-wide
leader queue. Public and managed waiters for one cohort share an admission; a
different cohort waits for the current generation and then wakes only itself.
One outer queue allowance covers that wait and any later generation, with the
caller's earlier deadline retained. Cancellation releases the caller's slot
while the bounded leader can finish for other callers. Expired callers cannot
start a new leader. The exact deployment waker supplies that deployment's scope.
Managed admission respects the app's ceiling and uses the admitted routing view
for the existing rollout allowance. Cached resident capacity includes cohorts
outside the current weight roster until lifecycle eviction; these still count
toward admission and its reported observation. Queue/drop/timeout refusals retain
the public structured problem and Retry-After contract. Scheduler ownership,
plan limits and the rollout grant remain unchanged.

Local Postgres integration combines the real policy reader, deployment/app
projection, shared handler gate, backend publication and managed proxy. It checks
stored queue depth, cancellation, coalescing, retained selection through cutover,
the steady app ceiling and a fresh request to the new cohort. Scheduler lifecycle,
source identity and final forwarding are fixtures. Configured cold daemon paths,
cross-process exact-wake coalescing, native lifecycle and deployed acceptance
remain unqualified.

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

Named compute gateways publish a small, credential-free runtime observation
after traffic handlers are wired and their listener is ready. A database
generation fences each process: registration compares the previously read
generation, resets its observation, and subsequent reports and retirement
must match the allocated generation. Retrying an ambiguous registration may
recover that same process token but may not reclaim a replaced generation.
Reports use database timestamps and expire after ten seconds. Membership is
the existing active compute gateway roster; absent and older daemons remain
visible as unverified. An unnamed development gateway has no fleet claim.

The observation describes actual wired retry enablement, rate-counter mode,
retry-counter mode and endpoint identity, deadline signing, verified policy
and security-fence wiring, and the managed HTTP/circuit surfaces. Customer
status reports fresh observations and mixed/disabled/local exceptions
separately from policy revision application. It does not promote capability
maturity or infer a successful counter operation, public-hop enforcement,
node admission, clock/key agreement, or native acceptance from wiring alone.
Those observations and complete-path checks remain required for release.

Policy acknowledgements must use that same process generation. A separate
bounded row per node/component records app/traffic, edge-rule, CORS-preset and
response-cache-purge progress. Publishers lock the current epoch before writing;
a replaced process cannot update its successor's progress. Readers, convergence
barriers and ledger pruning accept only the current generation, with a fresh
serving-process report. Legacy unfenced watermarks cannot prove convergence or
authorize pruning. No legacy cursor is relabeled as a new process acknowledgement.

The daemon captures its registration baseline before starting repair consumers;
it registers and reports serving wiring only after binding listeners. Repair
consumers may replay before registration, but publish only through the shared
process session. Each component keeps its independent ledger position. Cache
purge restart bootstrap may resume a previously applied durable position from
the fenced ledger; the new process must still replay before publishing its own
acknowledgement. Older daemons remain unverified during the upgrade and hold
pruning rather than manufacturing fresh progress.

Read-only policy preview uses the runtime's numeric budget resolution and
stored retry compilation. API write-time defaults remain distinct from
compiling an existing row. Request-header selectors use the immutable ingress
header snapshot; header actions can change execution overrides but cannot
manufacture a later selector match. A total-deadline rule is selected on the
original public route after owner resolution and pins the later execution
rule across rewrites. A deadline field on a rule reached only after rewriting
does not create an ingress deadline. Preview reports that selection even when
a later deterministic response or unavailable runtime gate ends its trace.
All millisecond values are clamped numerically before conversion to duration,
including request overrides, so a large integer cannot wrap into a short
positive timeout. Preview does not infer operator enablement, live accounting,
or deployed enforcement from entitlement or configured policy; those still
require fresh observations and acceptance evidence.

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
check below. Complete scoped/global host binding projection and runtime/preview
agreement remain delivery work; an account-wide byte quota must not silently
replace the per-host runtime bound.

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

Environment clones retain the repeatable-read policy snapshot and source/app
lock order. A source writer that commits after that snapshot can produce a
PostgreSQL serialization failure. The clone rolls back the complete attempt,
releases its session locks and pool connection, then retries a fresh guarded
transaction at the existing mutation retry cadence within the caller context.
Only SQLSTATE 40001 retries; quota, projection, ownership and other write
refusals remain terminal. An expired retry leaves no target catalog, copied
values/references or runtime stamp; successful recovery still checks every
projection before committing.

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

The in-memory canonical estimate searches encoded strings for quote candidates
in bulk, with backslash parity deciding whether each quote closes the string.
Quoted separators and exponent text remain excluded from the JSONB whitespace
and numeric expansion allowances. This avoids a byte-at-a-time pass over large
HTML-escaped policy bodies; serialization, conservative size estimates and all
analysis ceilings retain their existing meaning.

Route creates and replacements also check the enabled route-only discovery
projection across accounts. Positive traffic writers acquire a session advisory
lock for their account on a pinned direct-pool connection; route writers first
acquire the shared global-route session lock. Both locks precede the start of
the repeatable-read transaction and its account row lock. A first-statement
transaction advisory lock would freeze the snapshot before acquiring the lock
and could miss the previous writer's commit. Contenders release any acquired
locks and the connection before retrying; ordinary non-route mutations retain
account isolation. Commit/rollback release the session locks, and an uncertain
lock grant or failed unlock closes the session rather than returning it to the
pool. When apid uses a separate direct DSN, its hub-enabled direct pool
reserves three connections: one for the notification hub, one for the outer
edge-mutation convergence lock (the API process mutex admits one), and one for
the guarded transaction. Other daemons retain their existing direct budget.
An explicitly pooled control plane must include this separate API pool in its
operator capacity budget; the deployed compute-pooler topology leaves control
plane queries on their existing ordinary direct pools.
Both projections use repeatable-read transaction views so a concurrent
shrinking write or cascade cannot subsidize an increase between the before/after reads. Snapshot conflicts
while acquiring the account lock retry before intent is written; later database
conflicts roll back normally. The existing owned verdict precedes the global
verdict, preserving its refusal scope. Global aggregate/analysis refusals have
explicit global-route scopes and retain the existing structured API codes.
The global projection transfers scalar rule measurements only and uses the same
per-host language, compiler/default estimates and resource ceilings. Presets,
overlays and other rule kinds do not enter route-only discovery. MemStore runs
the global check under its existing mutex after the owned check. This first
global guard conservatively bounds the complete route-selector language;
claimed/reserved hostname exclusions and newly unclaimed scope transitions
still require integration with the actual hostname binding projection. It is
not release acceptance for the complete synthetic route path. Unsupported
legacy route action shapes refuse global route writes until replaced, disabled
or deleted; non-route writes in other accounts retain their owned scope.
Snapshot ordering follows the [PostgreSQL consistency-check rules](https://www.postgresql.org/docs/16/applevel-consistency.html).

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
a new oversized scope. App creation, quota/activity creation, preview batches
and set replacement, project apply/reconcile, restore (including activity), and
status updates that can reactivate a tombstone and visibility changes that
publish an internal app acquire that same account lock
before app/FK locks and retain it through the verdict. Related activity outbox,
cron and project changes commit only after the aggregate check. Parking/waking
already registered apps keeps its existing hot path. Removed environment/app
URLs cease to be serving scopes and cannot introduce an overload by dropping
their former app filter. Existing create conflict/quota, restore grace/claim
and compare-and-set predicates remain in force. This registered-URL eligibility
uses runtime status and public visibility, including status-only reactivation
with a retained historical deletion timestamp. Ordinary primary-hostname
activation follows the integration below. Alias/domain activation and complete
global discovery/binding agreement still require their
own integration and evidence. The in-memory aggregate mirror uses the same host
analyzer under its existing mutex. Proposed rule, preset, overlay, environment,
clone and app activation inputs are projected before publication; project
reconcile keeps its existing rollback maps through the verdict. Canonical reads
retain the conservative JSONB estimator already used by MemStore's individual
write bounds, while compiler inputs measure actual typed Go JSON (including
compact RawMessage numbers) and distinct referenced preset rows. The shared
environment projection supplies synthetic identities and replacement semantics.
The same phase timeout and input/metadata/automaton ceilings apply. No database
numeric expansion is invented for the in-memory compiler. Refused operations
retain related app/project/cron/preview/activity intent.
MemStore's runtime matcher also shares the analyzer's exact-or-SQL-LIKE parser,
including translated star/question-mark selectors, percent/underscore,
backslash escaping and Unicode. Exact/global/plain-suffix matches keep their
fast paths; unsupported resource use refuses rather than matching unverified
input.

Imported documents also validate canonical runtime bytes before replacement;
the existing import-body cap alone cannot bound JSONB numeric expansion.
JSON imports retain their source number tokens. YAML imports normalize through
the same parser as API validation before the JSONB projection check and save;
normalized expansion is bounded, while upload size/hash metadata remains tied
to the source. Both plain and quota writers use this representation.
Replacing an owned import reuses its account quota slot, including repair of
an oversized legacy document; unavailable plan tiers still refuse imports.
The quota decision remains serialized by the account lock. Environment cloning
validates copied target projections in its transaction before commit, including
app-wide fallback routes and the target slug's bytes. Failure rolls back the
entire target configuration. The in-memory clone preflights all projections
under its existing mutex before creating target state. Global aggregate
validation, complete primary/alias/domain binding transitions,
and complete-path recovery acceptance remain pending.

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

Primary app URL activation also joins the owned before/after projection. The
store takes the configured apps domain from its app writer; API and GitHub
preview/reconcile writers receive the same manifest DNS value and environment
override as routing. Empty disables that namespace. The ordinary primary URL
retains all matching account rules and distinct presets; it does not apply
the named-environment app filter. New publication/restore starts at zero and
removal cannot fall back to global discovery while its slug remains reserved.
Runtime and management share suffix normalization, one-label extraction and
the higher-priority immutable environment/deployment parsers. Primary markers
are literal strings, including legacy selector metacharacters in slugs. Their
metadata joins the existing bounded scalar SQL projection. This initial primary
activation guard includes potential legacy tag-prefixed app URLs conservatively;
alias shadowing/activation, domain binding transitions and operator namespace
changes still need the complete binding projection and acceptance evidence.

Alias publication joins the account-locked before/after projection as well.
Its URL uses the configured apps suffix and the existing app UUID/name label.
The projection mirrors routing's public owner, deletion metadata and allowed
target-status predicates, including retained superseded revisions. Ordinary
alias URLs retain account-wide matching rules/presets. A newly serving alias
starts with no baseline even when its selector was already oversized. The
alias row and its verdict share one transaction; app publication/restore also
sees existing aliases in that projection. Alias hostname collision lookup is
inside the same transaction. MemStore projects proposed alias rows under its
mutex. This publication integration does not complete legacy alias shadowing
or deletion/fallback transitions; those still need the complete binding
transition projection and all relevant writers.

Deployment status writes that can revive an attached alias also use account
serialization and the alias before/after projection. The mark-live transaction
keeps its existing app/deployment cutover, cron, snapshot and activity writes
inside that guard. Generic positive status writes preserve the cancelled-state
fence. MemStore checks a proposed target status before publishing any associated
intent or webhooks. Already eligible target transitions retain the incremental
repair baseline; failed-to-eligible transitions must fit the newly serving URL.
This does not add immutable revision URL projection or complete legacy alias
shadowing/removal, custom-domain or global binding transitions.
Builder, image and scheduler writers receive the same immutable apps-domain
configuration through TOML, its environment overlay and the manifest renderer.
Managed host roles supply that DNS value to their systemd units as well.

Ordinary custom-domain verification also uses the owning account's traffic
transaction. Exact domains are literal markers; wildcard domains use the
router's strict suffix language, including nested subdomains and excluding
the apex and literal asterisks. Each newly published domain/app binding starts
with a zero baseline even if another potential owned binding covers the same
host. Existing bindings retain the incremental repair baseline. The bounded
SQL projection initially included only verified, public, non-deleted ordinary
owners. The environment-scoped follow-up below adds named-environment domains.
App publication and restoration see the same domain markers.
The challenge, expiry and discovered app owner are repeated in the verification
UPDATE predicate. An expired, reclaimed or already verified challenge remains
a compare-and-set miss. Refusal rolls back verification, so certificate work
cannot follow a failed publication. Quota claim writers acquire the account
row before the app row, matching traffic mutation lock order. This conservative
owned projection does not resolve higher-priority binding shadowing, wildcard
fallback across accounts, domain removal/global discovery or tenant surfaces.
The DNS poller records publication success, stale proof, policy refusal and
store error separately from its existing TXT probe outcomes.
Compiler escape detection inspects string values and object keys in compact
JSONB when numeric formatting expands the body. Small ordinary rows retain
their text checks. JSON string formatting expands by at most six bytes per
input byte; larger amplification selects the compact inspection. Numeric-expanded
text is not scanned repeatedly just to establish that it has no HTML or Unicode
separator escapes. Canonical and compiler byte measurements, escape expansion,
defaults and phase limits retain
their existing meaning; no policy body is transferred for this analysis.

### Follow-up: environment-scoped custom-domain policy

Verified environment-scoped domains now select the same app-filtered policy
and optional headers/CORS replacement as the stable environment URL. This
supersedes ADR-233's application-owned custom-domain edge-policy limitation
for domains with an explicit environment binding; ordinary domains retain
their existing policy. Authoritative public compilation resolves each host
with the configured router in the same snapshot before selecting its overlay.
An alias or higher-priority tenant binding does not inherit a shadowed domain's
environment. Missing or failed environment reads refuse compilation.

Domain metadata includes the environment identity only when the app and
environment share an account/project. Publication, visibility/restoration and
rule/preset/overlay mutations validate every potential owned binding at each
matching host. The bounded analysis checks overlapping exact/wildcard scopes
separately, including ordinary and generated environment scopes. A new
domain/app/environment binding has no prior serving-policy allowance.
Raw owner-rule projection bounds apply before app filtering; compiled bounds
include only that app's retained rules, overlay and distinct retained presets.

Public policy hostnames are limited to 253 bytes, matching custom-domain DNS
validation; overlong input refuses before policy reads/compilation. Overlay
estimates reserve the full sixfold JSON escape allowance for that host length,
so exact and wildcard domain spellings cannot enlarge a compiled overlay beyond
its estimate. This is a centralized input bound, not a new account quota.
Cross-account shadow/fallback transitions, tenant surfaces and global binding
publication/removal still require the complete binding projection.
Wildcard lookup uses literal suffix comparisons in both direct and snapshot
SQLC reads, with the shared Go matcher's whitespace and single trailing-dot
normalization and case folding for supported ASCII DNS names. SQL LIKE operators do not widen a
legacy wildcard suffix. Most-specific ordering uses byte length, with lexical
ties, including unverified reservations. HTTP routing and cached-domain
validation reject asterisk-bearing request hosts; domain management can still
read wildcard claims by name.

### Follow-up: custom-domain removal and newly exposed policies

Removing an exact or wildcard custom-domain claim must validate the policies
it exposes before publishing the deletion or its activity. Domain selection
uses exact claims before the most-specific literal wildcard, including
unverified claims that block fallback. A newly exposed domain/app/environment
binding has zero prior serving allowance. Platform URL parsers take precedence
over custom domains. This negative transition includes fallback owners in
other accounts, without transferring action bodies between owners.

The removal transaction first acquires the global route session lock and
affected account session locks in sorted order, then account row locks, before
its repeatable-read snapshot. Bounded, secret-free domain metadata discovers
the affected owners; discovery and the original app owner are repeated after
locking. Contenders release all locks and the connection before waiting.
Existing owner policy is read one account at a time; before/after domain
bindings are reconstructed from the two claim projections to bound memory.
An owner-bound removal seam carries the API's authorized app identity into
both ordinary and activity writes. Authoritative API adapters without that
seam refuse removal; compatibility adapters retain their existing optional
activity path. Refusal preserves the domain, default
selection and outbox, and emits no deletion notification or audit event.

Global route analysis subtracts the public resolver's reservation language:
stored app slugs in the one-label apps namespace, tenant hostname claims,
exact/literal-wildcard custom-domain claims, and the syntactically reserved
tag, environment and immutable revision namespaces. In the apps namespace,
slug reservations take precedence over custom/tenant claims. A newly unclaimed
hostname has zero global serving allowance. The namespace automata must agree
with the shared hostname parsers, including UUID encoding and ordinal bounds.
Reservation metadata uses the existing input, byte, SQL and automaton budgets.
Potential global-route owners join the affected account lock set, and their
retained policy must fit a newly unclaimed scope even when the discovery rule
itself fits. The complete owner-check loop shares one analysis allowance.

Removal errors presented through the API expose no foreign policy witness or
exact foreign counts; limit refusals report a lower bound for the observed
value. Repair followed by deletion retry remains available. Tenant-surface
activation/removal, namespace reconfiguration and other binding writers still
need their own complete transition guards. Custom-domain fallback validation
covers its potential serving state with tenant surfaces disabled as well as
when enabled; an active tenant shadow may therefore cause a conservative
refusal until the exposed policy fits. This does not establish the complete
tenant-binding projection or its release acceptance.

### Follow-up: custom-domain publication and claim handoff

Creation, expired-claim reclamation and verification use the same bounded
binding transition as removal. A pending claim reserves its name and blocks
fallback even before it can serve traffic. Every transition therefore takes
the global route lock followed by sorted locks for overlapping claim owners,
the destination account and global route owners. Discovery is repeated after
locking, and the before projection starts only after those locks are held.
Each affected owner's policy is checked against the authoritative exact and
most-specific wildcard claim selection; a newly serving binding receives zero
prior allowance. A safe shadowing change may reduce existing exposure.

Verification retains the original app identity and challenge-token predicate
through a lock wait. It cannot rebase to a reclaimed claim. Creation retains
its destination app account and repeats that ownership check under the locks.
Quota checks, claim publication and optional activity remain in the guarded
transaction; refusal rolls them back together. The in-memory store applies
the same before/after projections before publishing a row or activity entry,
and observes cancellation. This closes custom-domain writer coordination;
other reservation writers and real daemon acceptance remain separate gates.

### Follow-up: serving tenant-host policy bounds

Owned host analysis includes verified hostnames on active tenant surfaces whose
app belongs to the same account, is public and is not deleted. A suspended
platform tenant contributes no serving scope. The binding identity includes
the hostname row, surface, app and optional platform tenant; new activation
cannot inherit a primary URL, custom-domain or earlier tenant binding allowance.
Tenant hostnames are exact, case-insensitive claims, with platform URL parsers
taking precedence. Bounds cover the potential tenant-enabled serving path;
configuration-off does not permit publishing a policy that cannot later serve.
Public snapshot tenant surface, hostname, binding and reservation reads retain
the database's case-insensitive hostname operator. Explicit text parameters
must be converted to citext so the snapshot agrees with native routing reads.

Direct hostname verification, surface activation, tenant reactivation and
surface linking use the existing account transaction and before/after bound.
Challenge verification additionally carries the observed token and original
hostname/surface identity through lock waits. The DNS poller requires that
challenge-aware seam before publishing and emits no verification notification
for stale or refused publication. Stored challenge proofs never enter policy
analysis metadata. The in-memory mirror validates proposed rows before writes.

This is a prerequisite for complete tenant transitions. Bulk apply,
reconciliation, offboarding and all cross-account shadow/removal/reservation
writers still need the shared complete binding projection and acceptance.
Security withdrawal and negative transitions retain their existing semantics
until those writers are guarded; this change does not accept the release gate.

### Follow-up: tenant binding transitions across owners

Tenant hostname creation, verification and removal, surface state changes and
linking, tenant reactivation, bulk onboarding, reconciliation and offboarding
share a bounded binding transaction. It discovers overlapping custom-domain
and tenant owners plus global route owners, acquires the global routing lock
and sorted account locks before its repeatable-read snapshot, then repeats the
discovery. A changed owner/hostname set retries before writing intent. Domain
publication/removal joins this coordination for overlapping tenant owners.

The projection checks both tenant-enabled and tenant-disabled routing. With
tenants enabled, an active verified public tenant binding precedes ordinary
and wildcard domains; a suspended tenant claims its hostname and blocks those
fallbacks. Pending/inactive/unverified tenant surfaces retain global hostname
reservation but allow domain fallback, matching the router. Platform namespaces
precede all tenant claims. A newly exposed owner/app/surface/hostname/tenant
identity has zero old binding allowance. Foreign action bodies, tokens and
display names never enter binding discovery or customer errors.

Bulk changes validate their final proposed topology before committing links,
credentials, quotas, webhooks or receipts. Dry runs and reconciliation plans
evaluate that topology without publishing it. Memory stores stage the same
proposal under their mutex; refusal and cancellation preserve all related
intent. Hostname deletion repeats the HTTP-authorized surface and originally
observed hostname row identity after lock waits.

Direct platform-tenant suspension remains the immediate security withdrawal:
it retains its hostname claims and blocks tenant/domain dispatch. Atomic
offboarding also releases managed hostnames and can therefore refuse a newly
exposed oversized fallback. Suspension remains available independently of that
cleanup. Broader app/alias/revision/operator and real daemon/native acceptance are
still separate release requirements.

The HTTP surface deletion cascade is one guarded transaction: soft deletion and
hostname removal are validated as the final topology and commit together. An
additive owner-bound store seam carries the authorized account and surface;
authoritative HTTP writers refuse when that seam is unavailable. A refused
cascade publishes neither partial hostname cleanup nor an audit event.

### Follow-up: app eligibility and retirement across binding owners

App visibility/status changes, tombstoning, cleanup, restore and namespace
publication can change tenant/domain eligibility or remove reservations. App
writers must discover the account's affected tenant and domain host languages,
join the same global/sorted-owner coordination, and compare authoritative
before/after topology in both tenant modes. Internal visibility and deleted
status are withdrawals of an app binding, but can publish a foreign fallback;
they therefore require analysis before publishing intent or activity.

Memory writers stage app and reservation changes before cancellation of jobs,
crons, builds or cleanup handoffs. Native writers keep that cleanup and its
activity inside the guarded transaction. Physical purge must account for
cascaded domain and hostname removals. Restore/new public bindings retain zero
prior binding allowance. Refused changes return existing typed traffic problems
with foreign witness/count privacy and preserve the complete lifecycle intent.
The immediate emergency security generation remains independent of routing
cleanup. App, alias, revision and operator coverage is complete only after each
writer and resolver path has matching evidence; native and fleet acceptance
remain separate gates.

### Follow-up: project and preview app batches

Project reconciliation and PR preview replacement can withdraw several app
bindings and publish others in one operation. They join the same global and
sorted binding-owner coordination as direct app lifecycle writes and validate
the final topology once before commit. Intermediate app order must not decide
whether the final batch is safe. Native app cleanup, cron replacement, project
metadata and preview receipts remain inside the guarded transaction.

Memory batches stage their app map, quota counts, cron map and metadata under
the existing mutex. App construction uses the proposed app map for uniqueness
and quota, then the full proposal receives binding analysis before any map or
receipt is published. Refusal and cancellation retain original app identities,
leases, tombstone slugs, cleanup and receipts. Creation-only preview batches
follow the same staging rule. Private service address allocation (ADR-576)
follows successful traffic and capacity validation, so refused app writes do not
advance allocation cursors or reclaim a prior address. Real fleet and native
acceptance remain separate release gates.

### Follow-up: account retirement and captured app ownership

Physical account retirement validates its complete routing cascade through the
shared binding guard before erasing intent or recording deletion. Discovery also
includes hostnames linked through an account's apps, platform tenants and legacy
redirect targets. These private ownership identities stay within the existing
metadata and input bounds; they carry no credentials or action bodies.

Native cleanup, legacy redirect-domain removal, the pending-account sentinel and
the deletion audit commit in one guarded transaction. Memory cleanup projects all
owned apps, account policies, tenant links and reservations together before the
first deletion. Refusal and cancellation retain the account and related intent.
Restoring a pending account remains independent of refused cleanup.

App guards that captured an owner repeat that predicate while locking the app
row before mutation. A changed owner refuses and rolls back; node reassignment
does not change customer ownership. Unsupported direct SQL remains outside the
coordinated publication API. Fleet, staging and native acceptance remain open.

### Follow-up: alias reservations and legacy primary fallback

A stored deployment alias reserves its exact configured apps-domain hostname,
even when its owner or target is temporarily unavailable, failed, cancelled or
soft-deleted. Resolution may return a routing miss, but that existing alias row
must not become a different app's primary hostname. A genuinely absent alias
retains the historical primary-slug fallback. Terminal pipeline outcomes can
therefore be recorded without publishing a foreign fallback.

Alias-row removal and physical app/account cascades join the shared sorted
binding-owner guard. Bounded private discovery includes alias reservations and
potential tag-prefixed primary slugs. Before/after owner views apply alias
precedence before assigning a primary host's legacy allowance. A newly exposed
foreign primary starts with zero serving allowance. Alias publication and
retargeting retain the existing active-target, collision and incremental-repair
contracts, with captured app ownership checked under the app row lock.

Memory writers analyze proposed alias changes before publishing them. Native
alias mutations, cascade cleanup and the verdict commit in one transaction.
Production alias-reservation reads participate in the same public host snapshot
and its fingerprint; an unavailable reservation read refuses resolution. These
changes add no plan limits or customer API fields. Immutable revision URL and
remaining writer/resolver coverage, fleet/staging and native acceptance remain
separate requirements.

### Follow-up: immutable revision URL publication

The aggregate projection includes every currently routable
`deploy-{revision}-{slug}.gregale.dev` URL. These URLs retain ordinary
account-wide rules and referenced presets, independently from the configured
primary/alias apps domain. The shared hostname writer/parser preserves existing
runtime grammar; only positive stored revisions on eligible public apps and
non-deleted pending/building/imaging/snapshotting/live targets participate.
Superseded, failed and cancelled targets do not serve revision URLs. The
immutable namespace remains reserved when its target is unavailable and never
publishes another hostname's fallback.

Deployment creation and creation with activity join the shared binding guard.
The native transaction serializes account/app policy before allocating the
revision and superseding an older pending row. Memory creation stages both rows
and checks the complete proposal before publishing either or its activity.
A new revision hostname receives zero legacy allowance. Positive status changes,
mark-live paths and app restoration/visibility/rename see the same revision
projection; an unchanged eligible hostname keeps incremental repair behavior.
Dark promotion uses the guard without altering its existing eligibility fence.
Native deployment mutations repeat captured app ownership and deployment
membership under the established account/app/deployment lock order.

Revision identities join the existing metadata, input, automaton and phase-time
bounds; no plan limit, schema or customer configuration field is added.
Creation refusals use the existing traffic-policy problems and preserve pending
rows and activity. Remaining writer/resolver coverage, path agreement, real
fleet/store/load/recovery, staging and native VM/network acceptance remain open.

### Follow-up: bounded request decision evidence

Ordinary response completion checks the attached budget's wall-clock expiry as
well as context cancellation when sealing decision evidence. A transport or
socket deadline can finish before the context timer exposes its error. Successful
detached streams retain their independent lifetime cause and do not inherit this
expired handshake verdict.

Public-handler requests and managed HTTP service calls receive a separate,
request-owned decision record at entry. Fixed fields report the traffic path,
last phase, final outcome, forwarding attempts/replays, retry stop reason,
rejecting limiter scope, cache serving outcome, endpoint circuit verdict and
local policy/body/wake/capacity/backoff durations. The record joins the existing
request/dependency span and public request log; the existing policy fingerprint
and verified deployment attributes remain the revision and routing evidence.

Fields use closed vocabularies and bounded integers, with no event list,
customer selectors, URLs, header values, bodies, credentials or error text.
Forwarding attempts count proxy dispatches, not guest side effects or scheduler
wakes. The first retry refusal is retained if it reduced the attempt ceiling.
A retry cannot allocate another record. A child service request owns a
new record; detached cache refreshes cannot change the parent's evidence.
Span finalization seals the record before exporting so late asynchronous work
cannot revise a completed decision. Public logs take a copy at their existing
observation point; a later lifetime cancellation may still change the final
span outcome. Telemetry remains observational and cannot change
admission, retry, cancellation or cleanup behavior.

Explicit platform reasons take precedence. Otherwise a refused pre-forward
response reports its last phase and status, rather than inferring a specific
cause from a guest response. A dispatched HTTP error remains an upstream
response; cached origin errors and configured fixed replies remain edge
responses. Forwarding does not prove application execution or rollback.
Durations measure this handler's work, coalesce overlapping measurements of
the same phase, may overlap across phases and do not establish a latency SLA.
A measured-phase list distinguishes unobserved
phases from measured waits rounded down to zero milliseconds. Successful stream
handshake detachment is recorded at its transport owner: an expired handshake
timer cannot relabel a completed stream, while lifetime cancellation and
security revocation remain observable. Policy revisions remain independently
protected response evidence; ordinary customer-authored spans are not platform proof.
Managed realtime, raw TCP and detached execution retain their separate owners.
Runtime/preview agreement and real daemon/native acceptance remain required.

### Follow-up: durable invocation version view

Durable and synthetic invocation resolution reads the app identity, project
release membership, scoped live target and direct revision eligibility from one
committed view. PostgreSQL uses the existing short read-only repeatable-read
transaction; memory uses its existing mutex. The transaction ends before enqueue,
wake or forwarding. The app projection contains only version-selection identity,
status and preview metadata, without environment, credentials or service policy.

A saved nonempty invocation account must still own the app in that view. The
scheduler carries that saved account through the trusted single-invocation HTTP
dispatch envelope; it is not reconstructed from guest headers. The optional
field preserves older callers and decoders that ignore unknown fields. Deleted
apps refuse even when no explicit pin is present. Older envelopes without an
account retain their existing trusted-delivery compatibility. Selected release
or revision headers remain canonical and are revalidated at delivery after wake;
an expired or withdrawn pin never falls through to newer code. Snapshot failure
refuses without a second pool-read resolution or partially published selection.

Full equivalence with public HTTP edge policy, shared public rate accounting,
managed realtime and trigger batches needs separate path evidence. Full
daemon/customer/staging and native VM/network acceptance remain required.

### Follow-up: synthetic target ownership and security lifetime

Normal managed synthetic HTTP delivery verifies the saved account, app version,
instance, node and scoped live deployment in the same committed invocation view.
Every target is checked, including unpinned standalone invocations. A scheduler
target is a claim until verified; a mismatch or failed snapshot refuses delivery
and cannot publish that target into the shared placement cache.
Standalone unpinned delivery retains scheduler selection rather than selecting a
new public traffic roster. Selected project graphs and revision pins remain fixed
across wake and are checked again with the returned target.

The gateway's existing security registry owns synthetic account/app scopes before
gateway-owned wake and deployment scope before forwarding. Pre-woken delivery
enrolls after the scheduler returns its target; the scheduler's wake lifetime
still needs separate complete-path acceptance. Nested target delivery joins that
lifetime and cannot release it early. Cancellation, missed revoke/release pairs
and store outages fence the exchange through forwarding cleanup; a late transport
success cannot turn a revoked invocation into a successful result. Production
wires this registry before starting the synthetic listener. Trigger batch envelopes
carry the trigger's saved account into every record, with the existing optional
field compatibility for older trusted callers.

Debug mirror replay retains its separately validated mirror target owner. This
change does not advertise public edge rules or public rate accounting for durable
background work. Node admission still applies to the synthetic HTTP forwarding
RPC. Complete path, fleet, staging and native VM/network evidence remain required.

### Follow-up: scheduler invocation wake and security handoff

The durable invocation drain enrolls owner account/app scopes before waiting on
wake, then verifies and enrolls the returned deployment before delivery. Its
request lifetime covers gateway dispatch and result handling, while the existing
shared wake leader retains its separate bounded lifecycle. Cancellation withdraws
this invocation's waiter; it does not cancel another caller's wake or promise
rollback of guest effects. Original scheduler context owns claim outcome writes,
so security cancellation cannot strand a writable claim until lease expiry.

Production schedd verifies the existing security store at startup and runs the
bounded periodic registry repair. A warm cached account allow is not security
admission. Target verification retains the invocation's version and owner view.
The drain rechecks its exact admitted generations after wake and before success.
Refused/unverifiable delivery uses the existing finite retry contract; a future
attempt receives a fresh security lifetime. Debug mirror replay remains excluded.

The trusted single-dispatch body carries a bounded canonical security baseline,
without guest headers or persisted customer configuration. The synthetic gateway
verifies that its scopes exactly match the resolved owner/app/target and admits
those exact generations before execution. A missed revoke/release during handoff
refuses instead of substituting the released generation. Optional absence keeps
older trusted callers compatible; updated gateway consumers must precede updated
schedd producers in a drained, matched-version rollout. Older consumers ignore
the new body field and cannot provide this contract.

### Follow-up: verified synthetic ingress auth policy

The three synthetic HTTP routes verify a fresh app ingress mode before wake or
dispatch, including pre-woken single invocations and empty batches. Production
wires the actual startup store before listeners serve. The SQLC projection reads
only `public_auth_mode` for an existing, non-deleted app; environment, credentials
and unrelated manifest data are excluded. The existing 250 ms service-policy read
bound applies within the inbound request's context, including pool acquisition.

A read error, expired context, empty mode or unknown mode refuses with the stable
`traffic_policy_unavailable` 503 and `Retry-After: 1`. Raw lookup errors are private.
A later attempt reads current state without caching either a refusal or an allow.
The declared modes remain `open`, `bearer`, `basic`, `ip_allowlist` and
`internal_only`. Only the last invokes the existing internal-service token gate;
this preserves the established trusted background-delivery authentication scope.
Workflow authorization retains its earlier admission order. Existing in-process
fixtures may omit the lookup; production always supplies the verified lookup.
The legacy string callback stays source-compatible but empty/unknown results
now refuse. Its absence is not a production outage fallback.

This is ingress authentication at the start of a synthetic envelope. Emergency
revocation retains its separate exchange lifetime. Public edge/rate/deadline
equivalence, per-record batch policy changes and full daemon/fleet/customer/staging
and native VM/network acceptance remain separate requirements.

### Follow-up: daemon fleet accounting acceptance

Local fleet acceptance must construct the handler through `runWithDeps`, using
parsed daemon configuration, real app/rule/public-policy reads and independent
Postgres pools in separate OS processes. It must observe the actual serving
generations and selected counter backends rather than manually arming a limiter
inside the test. Stored policy remains the source of rate and retry settings.

The local fixture uses the production node-client cache and forwarding RPC
client, with a fixture VM endpoint and one real HTTP origin. It verifies shared
app/account admission, charged cache hits, finite retry amplification, outage,
bounded lock waiting, recovery and process replacement. RPC transport failures
are injected before reaching the origin; application status errors retain their
separate no-replay contract. Placement, VM-side forwarding and guest execution
are fixtures, so this does not establish node admission, VM lifecycle, public
gateway transport, native network/KVM, deployed load or staging rollout.

Every public attempt must restamp the selected target's identity in its own
request headers and correlation context before invoking the forwarder. The
forwarding RPC reads the instance transport header, so selecting a sibling in
the picker alone is insufficient. Each attempt owns a cloned header map; app,
account, request and verified platform-tenant identity remain tied to the
admitted request while instance/node/deployment provenance comes from that
attempt's target. Empty provenance clears stale values from the first target.

### Follow-up: retry completion ownership

Public retry completion belongs to the last target whose forwarder actually
ran. A selected sibling refused before dispatch must not replace that owner.
Activity, per-instance request/egress records, debugger rows, completion logs
and logical request/forward spans use this completion target. The original
request remains one customer request and one app/account admission; forwarding
attempts retain their separate retry accounting.

The wake cause and cold outcome belong to the original admission. A sibling's
cached historical wake ID cannot replace that cause. Wake latency retains the
original wake node, while completion provenance identifies the last dispatched
instance. Streaming and upgrades remain single-dispatch paths.

Session affinity is stamped immediately before each actual dispatch so the
response selects the instance that served it. Buffered attempts inherit the
original response headers; discarding a failed attempt restores that baseline.
This preserves platform cookies alongside the final guest's cookies and prevents
cookies from a discarded failure from reaching the client. Local daemon fleet
tests must inspect the real debugger publisher's emitted RPC records as well as
guest dispatch. The receiver and VM endpoint remain explicit fixtures.

### Follow-up: bounded request evidence shutdown

Daemon cancellation quiesces ordinary debugger publishing while HTTP handlers
finish their bounded drain. Explicit publisher stop follows producer shutdown.
It cancels the current shipping RPC, retains an interrupted collapsed batch with
its original event IDs, and gives that batch plus queued rows one finite final
flush context independent of the canceled daemon context. A known success counts
once. Unconfirmed evidence at the final deadline is counted as dropped by represented
logical requests. The receiver's existing atomic event ledger deduplicates an
ambiguous acknowledgment replay. A lost acknowledgment can count as dropped even
when the receiver already committed the evidence. Financial usage retains its separate fsynced
outbox; optional debugger delivery remains best effort across process death.

Publisher stop joins the actor for every concurrent caller and cannot extend
its deadline for each batch. Stop before start is terminal and performs no work.
Shipping callbacks must honor their context, as the production gRPC client does.
No canceled guest request is resumed by the independent evidence flush.

The internal daemon shares one cleanup deadline across evidence publishing,
egress RPC shutdown, trace export and retained-span cleanup. It allows five
seconds after the 25-second HTTP drain, with that cleanup deadline capped at
30 seconds from drain start. Startup-error cleanup receives the same finite five-second
allowance. Publisher flushing uses at most two seconds within that shared
deadline. Consumers retain independent context cancellation. All operational
ceilings live in `pkg/api/limits.go`. Deployment must
verify service-manager stop settings and forced-termination behavior separately.
These context budgets do not bound every unrelated deferred resource close or
guarantee evidence from producers that outlive an exhausted HTTP drain.

### Follow-up: managed attempt identity and forwarding correlation

Every managed HTTP attempt owns a cloned request and header map, including
bodyless replays. The selected endpoint and verified account replace the prior
hop's identity in both guest headers and correlation context. Original causal
wake/invocation fields remain linked to the request. Upgrade uses the same
identity preparation and retains its existing single-dispatch semantics.
The logical managed-call span records the last endpoint actually forwarded;
an endpoint refused before dispatch cannot replace that owner. Empty endpoint
provenance clears earlier attempt values.

Both HTTP and raw forwarding RPCs explicitly publish the canonical request
correlation context. Publishing replaces the bounded reserved correlation keys,
including removal of stale empty fields, while retaining unrelated transport
metadata. Repeated preparation cannot grow duplicate identity values. Legacy
forwarding without a canonical context retains its existing transport metadata.
This correlation envelope is observation metadata, not a service authorization
credential. Managed authorization, pinned release selection, deadlines, security
fences and node admission retain their owners.

Actual configured gateway-process checks must verify managed caller authorization,
RPC/guest identity, shared retry debt and replacement behavior. Guest source
address and VM endpoints remain explicit local fixtures; native networking,
node lifecycle, fleet/load and staging acceptance remain separate requirements.

### Follow-up: fresh guest DNS caller identity

The guest DNS and HTTP listeners share the same fresh indexed HostIP identity
reader, scoped by the configured compute-node name. The ordinary instance
inventory stores node UUIDs, so filtering that inventory with a configured name
cannot establish caller ownership. A time-based source cache can also retain a
previous guest when a network slot is reused. Production DNS must not use that
inventory cache; each identity-dependent lookup reads current running/draining
ownership within the existing resolver deadline.

The SQLC source query groups deployment overlap by app before limiting the
result to two distinct app owners. Two deployments of one app cannot hide a
third row belonging to another app. An app with multiple or missing deployment
identities retains only its app identity; a deployment is returned only when
all eligible records agree. This keeps DNS discoverability scoped to the app
while release-graph dispatch requires its separate verified deployment identity.
Postgres checks cover sorted and hash aggregation, overlap and recovery.

Declared `.internal` aliases receive the private bridge answer only for the
current unambiguous caller and its current bindings. Unknown, ambiguous and
unbound aliases retain ordinary upstream DNS behavior. A source-store error
returns SERVFAIL without forwarding the private alias upstream; recovery reads
current ownership. HTTP independently authenticates the source and authorizes
the binding before dispatch. Guests may retain a DNS answer until its TTL
expires, so binding removal remains enforced by HTTP even with a cached answer.

Configured daemon-process tests exercise UDP and TCP DNS alongside managed HTTP
against Postgres, including different node UUIDs/names, exact address reuse,
node-scoped transitions, ambiguous ownership, running/draining versus inactive
states, binding removal and lookup outage/recovery. Source addresses, listener
binds, an ordinary DNS upstream and VM forwarding are explicit local fixtures.
These checks do not establish native namespace/NAT/firewall or DNS-gated egress
acceptance, outer daemon discovery, cross-host transport, deployed load or staging.

### Follow-up: readiness-aware service leases and attempts

The service endpoint registry projects only targets that pass the same current
readiness gates as public routing. An unready resident remains resident capacity;
discovery does not evict it or repeatedly hydrate a replacement. Independent
primary-app and sidecar sources retain their existing AND and event ordering.

The production PGBackend also validates exact instance, deployment, node and
effective port against its current local picker. Managed service leases refresh
when a cached target fails this check. Empty leases reread discovery so observed
readiness recovery is available without waiting for lease expiry. Every HTTP,
gRPC routing attempt, retry, Upgrade and private service TCP selection rechecks
eligibility before asking the circuit breaker, preserving its single half-open probe. A readiness
refusal is capacity evidence; a currently eligible target denied by its breaker
remains circuit evidence. Explicit retained zero-traffic revision pins remain
eligible when ready; ordinary routing still excludes them.

The private TCP proxy shares this selector and exact deployment wake path with
the HTTP proxy. A stale readiness, wake or node snapshot cannot be dialed;
recovery rereads the current endpoint without waiting for the lease. A pinned
zero-traffic deployment can be woken and served without routing ordinary calls
to it. This does not add raw TCP to the HTTP deadline or admission guarantees.

These are point-in-time local checks after this gateway observes a transition.
They do not cancel an already admitted exchange, guarantee instant delivery to
other gateways, or repair a missed notification. When some cached targets remain
ready, adding recovered replicas can still wait for the ordinary lease refresh.
Custom mutable endpoint providers must implement ServiceEndpointRoutability;
legacy immutable fixture providers retain their established lease behavior.

Local qualification connects durable primary/sidecar event inserts and their
existing PostgreSQL trigger to production LISTEN handlers in two configured
gateway processes and a replacement, then to production vmmd admission and
reusable bridges. It verifies cached withdrawal before lease expiry, source
ordering, empty-lease probes, recovery, retained resident capacity and replacement
hydration. Warm placement/readiness requirements, translated listeners/source
addresses, guest serving, namespace selection and VM startup remain fixtures.
All-unready ordinary wake, native readiness probes/VM networking, missed-notify
recovery, deployed load and staging need their own acceptance evidence.

### Follow-up: durable readiness repair and admission publication

Before publishing a newly admitted target, the production PGBackend verifies the
immutable deployment owner and readiness configuration through a narrow SQLC reader,
then reads the latest durable observation per required source. Unknown or unavailable
readiness reserves capacity without granting routing. The gateway writes no instance
state and does not bypass scheduler or VM ownership. Probe-free deployments keep their
existing eligibility after configuration verification.

The internal gateway owns a cancellable, joined repair worker independent of LISTEN.
It walks cached targets fairly using bounded retained scan memory, queries at most 128
targets per one-second tick and permits at most one second for each read. Required
sources remain ANDed, ordered by observation time and event ID. Newer notifications
survive an older concurrent read. Cache generations prevent a captured read from
mutating a replaced target or resurrecting an evicted one.

Successful durable verification renews a 30-second local lease. Read failures, missing
identity/configuration or required observations, and expired verification leases
refuse fresh picks and managed endpoint checks while retaining resident capacity.
Recovery makes the same instance eligible again. Verified deployments without probes
are excluded from recurring reads. Bounds live in `pkg/api/limits.go`. The SQL
projection excludes sidecar environment, image settings and probe command bodies;
only owner/deployment and readiness requirement fields leave that query.

Repair status exposes read start/completion, checked/unavailable counts and latest
observed event ID for qualification. A completed read barrier is separate from a
notification receipt. The local process fixture drops notification application after
the actual durable event/trigger/LISTEN receipt and locks its private deployments
table to exercise the production SQL timeout. Two gateways and a replacement recover
public and managed routing through real node admission/reusable bridges. Placement,
source addresses, native probes, namespace selection, guest and VM startup are fixtures.

This does not guarantee instant propagation, cancel already admitted work, discover
missing placement/lifecycle notifications, or establish actual hub overflow/reconnect
behavior. A large or slow repair backlog may fail closed at lease expiry. Native KVM,
network/firewall/leak acceptance, complete path/load/recovery and staging qualification
remain required separately.

### Follow-up: readiness and eviction belong to a VM lifetime

The routing lifetime is the app, instance, wake and node tuple. An instance ID
alone cannot certify a later wake or a migration's new owner. Production readiness
hydration and periodic repair use a bounded SQLC reader that filters this tuple
before selecting the latest observation per source. A newer retired observation
cannot mask the current source. Missing current observations still reserve capacity
and refuse routing. Identified targets also require the repair snapshot to echo
their wake and node identity.

vmmd retains the wake and its serving node on the live Manager instance. Recurring
primary-app loops capture these values when started, independently from the daemon
context. JailerVMM binds a guest stream's origin when preparing the VM listener,
before boot/restore; readiness dispatch rejects a stream whose origin differs from
the current live instance. Delayed frames cannot acquire a replacement Manager
entry's identity. Pool restore and migration adoption preserve the existing row's
wake through scheduler metadata; the destination vmmd supplies its own node.
Successful pool resume uses the existing forwarding reopen path to clear the pause
flag before starting recurring readiness. Instance and customer-intent ownership
are unchanged.

A new append-only migration adds wake/node fields to readiness notifications and
preserves the previous trigger on rollback. Source event caches are scoped by wake
and node. Modern terminal notifications carry the row's node and wake; a delayed
withdrawal from another wake or node leaves the replacement cached. Public forward
failures evict the captured target tuple. Quarantine stays 30 seconds, now centrally
specified in pkg/api/limits.go, and cannot block a different known wake/node.
Unknown legacy evictions retain their conservative fence. Managed endpoint leases
retain a private wake identity and cannot match a later wake of the same instance.

Unidentified legacy targets use only legacy observations; older notifications
without node/wake retain their compatibility behavior. A coordinated rollout must
migrate the trigger and update producers, then establish current identified probe
observations before enabling the stricter consumers. Historical unscoped rows do
not certify an identified VM. Compatibility behavior is not a lifetime guarantee
for legacy producers.

Local qualification uses actual PostgreSQL source queries, trigger/LISTEN payloads
and gateway consumers, plus real Unix listener replacement and gRPC metadata.
Scheduler placement, VM execution and native readiness probes remain fixtures or
unexecuted native paths. These fences do not discover missed placement/lifecycle
notifications, repair all stale database snapshots, or prove native migration,
pool/restore clock and entropy behavior, firewall or leak freedom. Native x86_64
Linux KVM and deployed recovery/load/staging acceptance remain required.

### Follow-up: authoritative repair of cached placement

The internal gateway owns a second cancellable, joined worker, independent of
LISTEN and request activity. A bounded SQLC statement reads current RUNNING
placements for rotating batches of at most 16 known picker apps. Each app returns
at most 128 candidates plus an overflow sentinel. Each cycle scans the current
known roster in serialized batches, without adding a tick delay between batches.
Missing/deleted apps and
ineligible/deleted deployments return an explicit empty snapshot. The projection
contains app/instance/deployment/wake/node identity, live/retired status, durable
node/deployment provenance and the scheduler's canonical runtime-port inputs.
It excludes historical terminal instances and unrelated manifest, environment,
image settings and credential data.

A complete snapshot removes absent targets, repairs moved/woken/port-changed
targets and discovers missed starts in live deployments. Known RUNNING residents
in retained superseded revision cohorts remain available to separately validated
pins, without discovering new retired cohorts or changing deployment weights.
An app generation and picker identity discard reads captured before concurrent
admission, eviction, quarantine, weight changes or picker replacement. Newer
readiness notifications survive a placement read; fresh identities read their own
readiness requirements and observations before eligibility. Unknown readiness
keeps capacity reserved. The gateway remains a reader of scheduler-owned rows.

The combined snapshot/readiness read has a one-second deadline and runs on a
one-second tick. Verified placements receive a 30-second local lease. Read
failure, missing/mismatched results, invalid rows or per-app truncation refuse
routing while retaining the cached resident count; incomplete data cannot prove
absence. A normal scheduler publication starts a bounded lease. Replaying its
unchanged identity and successful hot requests cannot renew the lease or clear a
failed verification. Public picks and managed endpoint validation share this
eligibility check, including deployments without readiness probes.

Qualification uses actual PostgreSQL placement/readiness queries against two
independent caches without notification subscribers, including private-database
table-lock timeout and recovery. Daemon startup-error qualification proves the
worker's query is canceled and joined. Membership transitions, guest execution,
native networking and deployed fleet recovery remain separate acceptance scopes.
Repair covers known picker apps. Completely unknown apps and a picker explicitly
removed by invalidation still depend on normal hydration/lookup paths. The legacy
live-target loader originally remained additive on request/notification paths;
the request-hydration follow-up below replaces that production wiring.
Large or slow scan backlogs can fail closed at lease
expiry. This is bounded convergence from a statement snapshot, not instantaneous
validation of every dispatched request or termination of already admitted work.

### Readiness before first publication and direct synthetic delivery

Production gateways require a stored readiness-configuration certificate before
a newly published target can serve public or managed traffic. Missing probe
fields mean unknown configuration, not a disabled probe. Certificates bind app,
instance, deployment, node, wake, normalized runtime port and complete required
source list; copying a target
and changing its identity cannot transfer verification. Bare exact-lifetime
replays preserve an existing certificate, readiness failure and expiry, without
renewing its lease. Unknown configurations join the bounded readiness scan.
Only an owner-matched snapshot can certify a deployment with no probes; that
immutable disabled configuration does not gain a recurring read dependency.

EnsureWarm, ordinary admission and the synthetic wake callback use the shared
bounded configuration reader before publishing scheduler results. Read failures
and missing observations retain resident capacity while blocking routing. Source
notifications received before configuration discovery remain scoped to their
captured lifetime and are merged after the complete required source set is known;
an older ready snapshot cannot mask a newer withdrawal from a newly discovered
source.

Pre-woken synthetic and debug mirror forwarding check the same stored source set
before invoking the node forwarder. Normal synthetic residents enter the public
cache even on readiness refusal, preserving admission accounting. Dedicated
mirror VMs remain outside the public picker and its resident count. Configuration
and observation reads inherit the request deadline and the existing one-second
read bound. These checks establish traffic eligibility, not termination of work
already delivered. Legacy optional backends without the stored reader/verifier
remain a weaker compatibility path; production wires both capabilities.

The implementation tracker is `docs/traffic_platform_implementation.md`.
Each guarantee needs configuration-to-runtime tests, cancellation/recovery
tests and customer documentation. VM lifecycle changes require native x86_64
Linux KVM metal and leak checks before acceptance. Fleet limits need real
multi-process/store tests. Code and local unit tests alone do not establish
deployed availability or control-plane HA.

### Request hydration uses current placement and source readers

Normal production restart/request reconciliation, RUNNING-notification refresh,
idle-aged validation and scheduler at-capacity recovery now use the same bounded
current-placement reader and complete readiness-configuration/source reader as
periodic repair. The historical slice loader is removed from production wiring;
its optional integration hook remains subordinate to the bounded reader.
An explicit reconciliation can discover a missed second cohort even while
another cohort is healthy. Current placements do not rewrite traffic weights.

Each app's placement and readiness reads share the existing one-second deadline
and 128-target completeness bound. Only a complete placement statement may
remove confirmed absent residents. Missing/partial placement or store failure
refuses routing while retaining cached capacity. A complete placement with a
readiness failure can remove confirmed absent entries while keeping discovered
residents unready. Disabled probes still require an owner-matched configuration.

Concurrent reads for one app coalesce. Each waiter retains its own cancellation;
a canceled waiter does not wait for another request's read. Caller cancellation
does not withdraw a shared cached target. Picker pointer and membership/weight
generation fence existing caches. Reads of an absent picker also retain an active
fence until completion, so a concurrent create/delete, eviction or weight update
cannot hide a change by returning the picker to absence. Those fences are removed
when the read finishes; a picker is published only after a complete snapshot.

Idle validation compares the full cached routing tuple, then the handler reselects
from current eligible targets within the already admitted deployment and affinity
scope. Validation errors return 503 before forwarding or wake admission.
Reconciliation errors also stop the cold/deployment wake path. Warm wire wake
correlation keeps its existing contract; cached targets retain their wake identity.

That request-hydration repair left authenticated unpromoted smoke discovery on a
separate historical instance lookup. The candidate repair below supersedes that
implementation; its acceptance is pending. Complete path, deployed load/recovery
and native VM/network qualification remain open.

### Deployment-smoke candidate discovery

The independently authenticated post-readiness verifier reads one current
candidate through a SQLC statement scoped to the app and requested deployment.
It filters running instances, matching deployment ownership, non-deleted apps
and non-deleted snapshotting/live deployments before selecting the newest
resident. Terminal history and sibling deployments cannot enter the result.
The narrow projection carries routing identity, durable provenance and the same
runtime-port inputs as ordinary placements; unrelated inferred-profile fields
are excluded. This is a single-candidate lookup, not a customer capacity limit.

Candidate placement and scoped readiness verification share the existing
one-second placement-read deadline and request cancellation. Missing, failed or
unready required sources return an error before the handler can forward or
admit another candidate. A complete absent result can still trigger exact
deployment admission. Disabled probes require an owner-matched stored
configuration. The verifier merges newer lifetime-scoped notifications before
returning a target. Candidate discovery does not publish into the ordinary
picker or resident count; the authenticated challenge remains app/deployment
bound. Optional integrations without a readiness reader retain their existing
weaker compatibility behavior; production supplies the stored reader.

Verification status and exact source/gate receipts are recorded in the
implementation tracker. The unchanged-production baseline reproduced readiness,
read-bound, cancellation and port-validation failures. Focused Go and actual
PostgreSQL regressions passed after the new fixture's commit-SHA value was
corrected to satisfy the existing schema constraint. Those checks qualify
store/cache/handler fixtures. Complete-path, deployed, native and release
acceptance remain open.
