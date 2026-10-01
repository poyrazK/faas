# Request traffic policy snapshots

## Durable invocation version selection

Async routes, the scheduler drain and synthetic delivery resolve app identity,
scoped release membership and direct revision eligibility in one committed
snapshot. PostgreSQL ends its read-only repeatable-read transaction before wake
or enqueue; memory readers share the store mutex. The version projection loads
only app/account/project identity, preview parent, status and deletion time.
It does not load environment, credentials, service policy or guest artifacts.

A nonempty saved invocation account must match the current app owner. Deleted
apps refuse even for unpinned envelopes. The scheduler preserves the saved account
through the trusted single-invocation HTTP dispatch body; guest headers cannot
replace that field. Older trusted envelopes with no account
retain compatibility. Project releases and explicit revisions require a snapshot
reader; custom adapters with only independent pool resolvers refuse pins. Minimal
standalone unpinned adapters remain supported. A failed snapshot cannot publish a
partial header selection or retry through independent reads.

Enqueue captures a canonical release/revision header; delivery revalidates it.
The gateway verifies every claimed instance, node and scoped live deployment in
the same snapshot, including unpinned targets. Invalid targets never enter the
shared placement cache. Expired or disabled pins refuse instead of selecting
newer code. Unpinned standalone invocations retain their scheduler selection
semantics. These checks establish version selection and owner validation. Full
public edge policy, public rate accounting, scheduler wake fencing and request
decision observations still need separate synthetic-path acceptance evidence.
Normal synthetic HTTP now joins the account/app/deployment revocation lifetime
through gateway-owned wake and forwarding cleanup; see
`traffic-security-revocation.md` for coverage and exclusions.

## Public request policy

ADR-375 resolves hostname ownership and pins the corresponding route-only
graph in one fresh readonly transaction before public route substitution.
Claimed hosts read only their owner's routes before applying projection bounds.
Genuinely unclaimed, substitutable hosts retain global route discovery; reserved
misses and immutable deployment URLs skip that unused lookup. The matcher uses
the public router's namespace configuration and refuses requests if production
ownership resolution is not configured. The private root claim is rechecked
before compiling the full owner policy, including namespace feature changes.
An intervening ownership or root-metadata change returns 503; a fresh request
can resolve the current claim. The hostname/app transaction then reads the verified owner's complete host rules,
referenced presets and stable environment URL overlay alongside app flags.
Compiler-cache reuse requires the same freshly read content baseline; missed
notifications cannot keep a stale policy active for a fresh request. The plan
table joins those inputs before security checks or an edge response.
External CORS presets and named-environment headers/CORS are included as
resolved actions. Imported OpenAPI method/path declarations and environment
route-contract overlays are pinned before the app snapshot is sealed. Route
enforcement and observed route labels use that same contract. Upload, wake and
retries retain those inputs even when shared caches refresh. App slices and
maps are copied before guest work.

Public hostname ownership and app/account settings now require a fresh,
read-only repeatable-read Postgres view, bounded to 250 ms. A warm route or VM
cache cannot replace that read. The projection excludes application env and
unrelated account credentials. Custom domains, wildcard reservations, tenant
bindings, aliases and named-environment membership are resolved through that
view. Ordinary project app hosts project production ingress; standalone hosts
project the default scope. Revision and alias hosts use their exact
deployment's ingress, including a retained live revision with zero ordinary
traffic. A different environment cannot change the primary ingress or sidecar
roster of that URL.

The same view reads an exact environment's declared-route overlay and, when
no explicit route list applies, the owner-scoped imported OpenAPI document.
A missing document is an authoritative result. Matching and observation
compile this private carrier rather than loading another store/cache
generation. Compilation cache hits require the same document content digest;
missed notifications cannot preserve an old contract for a fresh request.
Explicit routes keep precedence and do not read an unused imported document.
Canonical JSONB contract payloads are bounded to 512 KiB before transfer,
covering the existing 256 KiB import allowance plus formatting expansion.
Raw documents are excluded from serialized app and response/trace evidence.

Edge projections use sqlc and deterministic priority/creation/ID ordering.
SQL refuses over 50,020 matching rules or 64 MiB of canonical row JSON before
transferring the aggregate. Referenced presets count toward the compiled
input byte bound; each preset and environment overlay is limited to 512 KiB
before transfer. Claimed hosts do not load unrelated tenants' routes, other
rule kinds or presets, so foreign policy cannot exhaust their projection bound.
The compiled cache preserves the existing 10,000-host entry ceiling.
An oversized projection refuses with `traffic_policy_unavailable`/503.
Edge-rule creates and updates now validate the complete canonical saved row
against the 64 MiB host ceiling before commit. Mutation statements return only
the ID; a bounded sqlc read returns no policy body when it exceeds the ceiling.
This includes inactive action fields and JSONB numeric expansion. Refusal
rolls back the intent and transactional change event and returns the existing
`traffic_policy_too_large`/422 problem. Smaller replacements and deletion
remain available. This single-row check does not establish the combined host
rule and referenced-preset bound.

Rule and preset creates/updates, environment edge overlays, new environment
registration, environment clones, alias publication, positive deployment
status writes share account serialization. A session
advisory lock on a pinned direct-pool connection precedes the repeatable-read
transaction; its account row lock then precedes app/FK/policy-row locks and is
retained through commit. Route creates/updates first take the shared global
route session lock. Contenders release acquired locks and the connection before
retrying; their request context bounds the wait. Other rule kinds retain
separate account locks. Commit/rollback release session locks; uncertain lock
grants or failed unlocks close the session rather than returning it to the pool.
With a separate direct DSN, apid's hub-enabled session pool reserves three
connections for the hub, outer convergence lock and guarded transaction. The
API process mutex admits one outer mutation lock. An explicitly pooled control
plane must include this sibling pool in its database capacity budget. The
deployed compute-pooler topology keeps control-plane queries on their existing
ordinary direct pools.
This also serializes the existing account preset quota across different apps.
Ordinary owned host overlap validation uses that transaction boundary;
there is no account-wide policy byte quota.

Before commit, Postgres returns selector groups, counts, measured sizes and
referenced preset IDs for the account. Action bodies remain in the database.
A bounded automaton checks the same exact-or-LIKE host language used by the
runtime query, including percent, underscore, star, question mark and escaped
characters. Disjoint hosts can each use their own allowance. A referenced
preset contributes once per host, even when several matching rules share it.
The 50,020-rule and 64 MiB canonical projection caps are checked independently
from a conservative 64 MiB bound for the compiler's escaped JSON input. The
estimate includes Go defaults added during decoding, the mirrored preset ID,
HTML/Unicode escapes and timestamp spelling allowance. It can refuse a policy
close to the byte ceiling before the exact compiler size reaches that ceiling.

Each before/after analysis phase has a two-second allowance. Its SQL read uses
a local 1,750 ms server timeout so cancellation does not depend on client
connection cleanup; the previous statement timeout is restored on success.
Analysis also limits inputs to 100,000 groups/assets/environment/primary/alias/revision/domain/reservation identities, metadata to 64 MiB,
automaton nodes to 1,000,000, states to 100,000, retained state buffers/overhead
to 64 MiB and transitions to 2,000,000. A proved overload returns
`traffic_policy_too_large`/422. An exhausted analysis returns the distinct
`traffic_policy_too_complex`/422, with no saved mutation or change event.
Legacy action aliases whose decoding cannot be verified by this projection
also refuse analysis; replace the affected rule through the supported API
shape or disable/delete it to repair that graph. Caller cancellation still
ends its transaction and releases the account lock.

A legacy oversized host can be repaired incrementally when analysis finishes
and none of its over-limit dimensions increases. A proposed overload at a
different host still refuses. Named environment URLs also check the combined
app rules and overlay after replacement, including a separate post-filter rule
count. A missing overlay keeps app headers/CORS; an explicit empty overlay
suppresses them. Other apps and replaced presets are excluded from compiler
bytes while every matching account rule still counts toward the original
canonical read bound. Registered environment/workload identities split the
selector analysis at their actual stable URLs. New environment registration
and clones must fit their newly exposed URLs; legacy unknown-host overload
cannot serve as their baseline. App creation, quota/activity creation, preview
batches and set replacement, project apply/reconcile, restore, and status or
visibility publication now validate newly exposed registered environment URLs
inside that transaction. Refusal also rolls back activity, cron, project and
preview-set changes. Eligibility follows runtime status and public visibility;
a historical deletion timestamp alone does not exclude a reactivated app.
Removing a registered URL does not expose its former fallback. Ordinary
primary hostnames also join that activation check using the configured
`apps_domain`. Ordinary primary hosts retain all matched account rules and
presets. API and GitHub preview/reconcile writers receive the manifest DNS
value; `FAAS_APPS_DOMAIN` overrides their TOML. Empty disables ordinary primary
URLs. Immutable deployment/environment URL shapes remain separate. Alias
publication now joins this check using its stable app UUID/name URL and that
same apps domain. The alias write and verdict commit together. A new serving
alias must fit the host allowance, even if an existing selector was already
oversized; retargeting an existing URL retains the incremental repair rule.
App publication and restore also validate attached aliases. Their eligibility
requires a public owner with neither deleted status nor a deletion timestamp,
and an undeleted target in a routing-eligible status, including superseded
revisions. Ordinary alias URLs retain all matched account rules and presets.
Refusals preserve the alias row and return the structured 422 errors below.
An explicit empty apps domain disables alias publication scope as well.
Generic positive deployment status updates and mark-live also validate alias
revival. Their verdict shares the transaction with the status change and any
existing cutover, cron, contract snapshot or outcome activity writes. Cancelled
targets retain their terminal-state fence. In-memory writers check the proposed
status before saving intent or enqueueing webhooks. Already eligible targets
retain their repair baseline. Builderd, imaged and schedd receive `apps_domain`
through TOML, `FAAS_APPS_DOMAIN` and the manifest renderer. Managed host roles
supply the same configured DNS value to their units.

Ordinary custom-domain verification validates all matched owner rules and
distinct presets before publishing the verified binding. This covers exact
and wildcard domains; wildcard analysis follows strict suffix routing,
including nested subdomains and excluding the apex and literal asterisks.
A new domain/app binding must fit its full allowance, including when an old
selector or another potential owned binding already covers that hostname.
Already published bindings retain the incremental repair rule. App publication
and restore include their verified ordinary domains even when `apps_domain`
is empty. Refusal leaves verification and certificate intent unchanged; the
DNS poller reports the failure and can retry after policy repair. Challenge
verification repeats the observed token, current app owner and unexpired
deadline in the write; a stale challenge cannot publish a reclaimed domain.
Creation, expired-claim reclamation and verification take the global route
session lock and sorted account locks for overlapping claim owners, the
destination account and global route owners. The same binding transition guard
is used for deletion. Its bounded before projection begins after locking,
repeats owner discovery and uses exact and most-specific wildcard selection
across accounts. Pending claims reserve their hostname and block fallback;
safe shadowing changes can reduce existing exposure. Quota checks take the
account row before the app row and commit the claim and optional activity
together. Cancellation/refusal preserves the prior claim and emits no creation
audit or notification. Creation analysis failures use the existing structured
422 codes and a proven lower bound, without
foreign hostname witnesses or exact policy counts.
The DNS poller's `*_domain_verification_publications_total{outcome}` counter
distinguishes `success`, `stale`, `refused` and `error`. Existing
`*_domain_verification_results_total` reports TXT probe outcomes; a successful
probe alone does not mean verification was published. These counters have
fixed labels and include neither hostnames nor challenge tokens.

Explicitly environment-scoped custom domains now use the same app filter and
optional headers/CORS replacement as the stable environment URL. A missing
overlay retains that workload's rules; a present empty overlay suppresses its
headers/CORS. Publication and later rule, preset, visibility/restore and overlay
writes include those domain bindings in the guarded aggregate. Raw owner rules
are bounded before filtering; compiled bounds include retained rules, distinct
retained presets and the selected overlay. Overlapping potential owned bindings
are checked separately. A new domain/app/environment binding has no prior
overload allowance.

Owned analysis also includes verified hostnames on active tenant surfaces with
a public, non-deleted app in the same account and no suspended platform tenant.
The potential tenant-enabled path is bounded even while its runtime flag is
off. Hostname row, surface, app and platform tenant form the binding identity;
verification, surface activation, direct tenant reactivation and surface linking
cannot inherit an earlier binding's overload allowance. Platform URL namespaces
take precedence over tenant claims. Challenge tokens and tenant display names
never enter the bounded analysis metadata.

Those direct writers use the existing account transaction and commit guard.
The DNS poller repeats its observed challenge and captured hostname/surface
identity through account-lock waits. Stale or refused verification leaves the
claim unchanged and emits no verification audit. Direct platform tenant status
and link APIs return the existing structured 422 policy errors on bound refusal.
Repair the matching policy before retrying publication.

The public compiler resolves the actual binding using the captured router
namespace in the same snapshot. Exact ordinary domains, aliases and tenant
routes do not inherit an environment from a shadowed domain. Fresh requests
read the authoritative overlay before using a compiled cache entry; missed
notifications cannot retain an old policy. Unavailable or oversized scoped
reads refuse admission. Public policy input is bounded to 253 hostname bytes;
overlong hosts refuse before policy reads. Overlay byte estimates include the
maximum hostname length and JSON escape expansion, including wildcard hosts.
Direct and snapshot wildcard lookup use literal suffix comparisons, including
legacy percent/underscore characters. HTTP request hosts containing an asterisk
cannot activate or retain a custom-domain route; wildcard claims remain visible
to domain management. Exact snapshot lookup, reservations and verification
preserve the database's case-insensitive domain identity.

This initial projection includes potential legacy tag-prefixed primary URLs
conservatively. Exact legacy alias shadowing, deletion/fallback transitions,
immutable revision URL activation, bulk tenant apply/reconciliation/offboarding,
cross-account tenant shadow/removal/reservation transitions and operator
namespace changes still need the complete binding projection and acceptance.

Custom-domain deletion now checks newly exposed exact/wildcard fallback owners,
including owners in other accounts and explicitly environment-bound domains.
Exact claims precede the most-specific literal wildcard; unverified claims
continue to block fallback. A new domain/app/environment identity has zero
prior allowance. Global route discovery and its potential owner's retained
policy also need to fit on newly unclaimed hosts. Deletion takes the global
route session lock, sorted affected account session locks and account row
locks before its repeatable-read snapshot. Owner discovery is repeated after
locking. Bounded claim metadata contains identities and eligibility, without
challenge tokens, certificate intent or action bodies. Policies are checked
one account at a time within the existing analysis allowance.

The API carries the authorized app identity into the locked deletion and
repeats that predicate in SQL, so a name reclaimed while waiting cannot detach
the new owner's claim. Native stores commit domain/default deletion and its
activity together. Refusal preserves that intent and emits no removal audit
or notification. Aggregate or analysis refusal retains the existing 422 codes
and reports a proven lower bound instead of foreign counts or a hostname
witness. Repair the exposed policy, then retry; an operator may need to help
when the policy belongs to another account. This check conservatively includes
the potential fallback with tenant surfaces disabled, even when an active
tenant binding currently shadows it. Tenant transitions need separate guards.

Route creates/updates also check enabled route-only discovery across accounts
using the same per-host language and resource ceilings. Disjoint hosts retain
separate allowances; a connecting wildcard does not turn their union into a
flat global quota. Both owned and global before/after reads use the stable
transaction view, so concurrent deletion cannot hide an increase above a
legacy overload. Global refusals use `global_route_` scopes and the same
structured 422 codes. Unsupported legacy route shapes refuse global route
writes until replaced, disabled or deleted; non-route writes in other accounts
retain their owned scope. No action bodies are transferred for this analysis.
Global analysis now subtracts stored app slugs in the one-label apps namespace,
tenant hostname claims, exact/literal-wildcard custom-domain reservations and
the syntactically reserved tag/environment/revision namespaces. The apps
namespace takes precedence over custom/tenant reservations. Wildcard percent
and underscore characters are literal, matching the public reservation read.
UUID encoding and revision ordinal bounds follow the shared hostname parsers.
Newly unclaimed scopes start with zero serving allowance. Complete transitions
for every reservation writer and operator namespace changes remain pending.

Individual CORS preset creates/replacements, environment overlay replacements,
scoped route replacements and imported documents validate the complete runtime
object before saving. Postgres measures canonical JSONB bytes, including
ownership metadata and separator whitespace. CORS PATCH validation uses the
merged preset, so two individually small field updates cannot exceed the
preset bound after merging.
Display names, descriptions and timestamps are excluded from these projections.

An imported document must fit both the existing 256 KiB upload cap and the
512 KiB canonical runtime bound. Scientific numbers can expand substantially
in the stored JSON representation, so a small upload can still exceed the
runtime bound and return the structured 422 below. Replacing an existing
owned document reuses its import quota slot, including at the account limit;
creating another import still requires a free slot. Plan tiers without import
support continue to refuse writes.
JSON and YAML imports keep the documented format support. YAML is normalized
to JSON before projection validation and storage; expanded aliases must fit
the runtime bound. Upload size/hash metadata still describes the source bytes.

Environment cloning validates the actual copied target projections before
committing its transaction. The bound includes the target environment name
and app-wide routes copied as an explicit fallback. An oversized clone refuses
with 422 and rolls back its target environment, variables, secrets and policies.
Repair the source policy and retry the clone. A longer target name can make a
source policy already at the bound exceed it after copying. Provider-managed
binding creation and compensation retain their existing separate contract.

An oversized write returns `traffic_policy_too_large`/422 with `limit`,
`observed` and `docs_url`; byte limits also include `limit_bytes` and
`observed_bytes`. The existing policy
is retained. Reduce the values and replace the policy; an empty environment
overlay, a disabled empty scoped route policy, or preset deletion is also
available. Existing oversized rows can be repaired by a smaller replacement;
fresh bounded reads see the repair without relying on notification delivery.
Remove referencing CORS rules before deleting a preset; retained references
continue to refuse traffic, so deleting a referenced preset alone is not a
serving-policy repair.
The in-memory store uses a conservative bound and may reject a near-limit
object whose extra JSON string escapes are smaller in Postgres.
It now applies the same per-host aggregate analyzer under its mutex to rule,
preset, overlay, environment/clone and app activation writes. Its canonical
read estimate retains the existing conservative numeric/whitespace allowance;
compiler bytes measure actual Go JSON and distinct referenced presets. Compact
RawMessage numbers are not expanded for the Go compiler. Rejected changes
preserve related project, cron, preview-set and activity intent.

MemStore also checks route-only global aggregates and domain creation,
verification and removal under that mutex before publishing intent or activity.
Direct tenant verification, activation, reactivation and linking also check
proposed rows before publication. Complete alias and bulk/cross-account tenant
binding transitions and namespace changes, global binding/synthetic path
agreement and recovery acceptance remain rollout requirements. These write
checks do not establish release acceptance.

Before dispatch, the public routing transaction re-resolves the host projection
and compares its private content fingerprint. Changed settings, alias/domain
retargeting, environment policy changes, deleted targets or a changed tenant
binding refuse dispatch with `traffic_policy_unavailable`/503. A policy read
failure cannot turn an owned hostname into a synthetic edge-rule host.
Changed imported documents or scoped route contracts also refuse an old
dispatch baseline, as do changed edge rules or CORS presets. Admitted requests
retain their compiled contracts and actions.
Both transactions finish before wake. Admitted requests retain their verified
settings and selected deployment during wake and retry.

An edge route substitution records both the source hostname and target app
baselines. The target transaction rechecks the source before a pure edge
response can run; dispatch rechecks both in the routing transaction. A genuinely
unclaimed host has a verified negative claim. Internal/deleted app slugs,
unverified exact/wildcard domains, tenant reservations and failed alias targets
cannot become synthetic hosts. Named-environment, immutable deployment and
resolved alias URLs retain their exact target. A reservation created between
the first lookup and dispatch refuses the old negative claim. Releasing the
domain reservation lets a fresh request resolve again. The alias namespace
remains protected after removal of an alias binding. Both baselines join the sealed
effective fingerprint, and source app deletion can revoke the admitted target
exchange. A selected route with a missing/unavailable target refuses with
`traffic_policy_unavailable`/503 instead of answering from the source app.
Preview/runtime agreement and full acceptance remain required.

## Runtime evidence

### Gateway wiring observations

`GET /v1/apps/{slug}/policy/status` includes `traffic_runtime` for the
authorized app owner. `gregale app <slug> traffic-status` renders the same
response; `--json` preserves policy revisions and observation fields together.
The existing optional API `wait` waits for policy convergence only.

App/traffic, edge-rule, CORS-preset and response-cache-purge acknowledgements
share that process generation and keep separate ledger positions. Status and
convergence waits accept only the current generation with a fresh serving
report and a fresh component acknowledgement. They use the database clock and
reject future timestamps. Replacement removes the prior process's convergence
until the new consumers replay and acknowledge their positions.

Ledger pruning uses the same ownership and freshness conditions. A missing,
stale, retired or older daemon holds pruning at zero while it remains in the
serving roster. Upgrade named gateways promptly and investigate prolonged
missing progress to prevent retained history from growing. Legacy watermarks
cannot prove convergence or authorize pruning. Cache-purge restart bootstrap
can resume a previously applied position from the fenced ledger; the new
process still replays before acknowledging it. A new node can resume a fresh
serving peer's purge position under the existing shared-cache assumption.

Named compute gateways report after handler construction and listener binding.
The database assigns a new generation at registration; replacement clears the
old report. Updates and shutdown retirement require that generation and process
token. A late process cannot reclaim a replacement by retrying its old
registration. Unnamed development gateways do not create fleet observations.

Reports run every 2 seconds with a 250 ms database deadline. Database timestamps
expire after 10 seconds; readiness failure removes the report. The reader uses
the active compute-only/compute-node roster with a configured gateway target.
Missing or stale members leave feature state `unverified`; partial freshness is
visible in the counts. The reader refuses a roster beyond 4,096 members instead
of returning a truncated fleet claim. Older daemons remain missing until upgraded.

`state=observed` means every roster member has a fresh wiring report. Per-feature
state is `observed`, `mixed`, or `unverified`. Modes show:

| Field | Wiring reported |
| --- | --- |
| `public_retry` | Public handler retry gate enabled or disabled; an app rule must still authorize retries |
| `rate_counter` | Central PostgreSQL or process-local account/app rate backend |
| `retry_counter` | Shared PostgreSQL, Redis, local, or unwired; differing shared endpoint identities report mixed |
| `deadline_signing` | A deadline signer attached to the handler |
| `policy_snapshot` | Public routing snapshot reader attached to the handler |
| `security_revocation` | Emergency revocation registry attached to the handler |
| `managed_http` | Tenant managed HTTP listener constructed and bound |
| `managed_circuit` | Adaptive endpoint breaker attached to that managed HTTP listener |

Shared retry endpoint identities contain no URL or credentials and are used
only for aggregation; the customer response contains no node names or process
tokens. The report describes wired components. It does not prove target
reachability, counter operations, rate backend endpoint agreement, signer key
agreement, public-hop availability, VM admission, or outbound firewall enforcement.
`enforcement_status` remains `unverified`. Capability maturity and release gates
require their own request-path, recovery, native-host, and staging evidence.

### Request policy fingerprints

Ordinary responses expose `X-Gregale-Traffic-Policy: traffic-v1:<sha256>`.
The gateway owns this response header at commitment; application responses,
edge header actions, and declared or late trailers cannot overwrite it. The request span carries
`gregale.traffic.policy_revision`, and request logs carry
`traffic_policy_revision`. Raw Upgrade response bytes do not expose this HTTP
response header; the request span/log still records the snapshot.

This is a versioned fingerprint of the effective inputs used by that request,
not a database transaction number or fleet convergence acknowledgement.
Different hosts or tenants can have different fingerprints. Compiler/plan-table
changes can change the fingerprint. Credentials and raw policy are never
included in response or log evidence. Live counter, picker, cache, and breaker
state are decisions made against the snapshot, not frozen counter values.

### Request decisions

The public routing handler's `gateway.request` span and each managed service
dependency span include a fixed `gregale.traffic.*` decision record. Public
request logs include the same fields in `traffic_decision`, subject to the
existing log level (hot 2xx requests use debug). Existing policy revision and
selected deployment attributes remain separate routing evidence.
Logs take a copy at the existing observation point; the span seals the record
at handler completion. A later lifetime cancellation can change the final span
outcome after the request was logged.

| Fields after `gregale.traffic.` | Meaning |
| --- | --- |
| `decision_version`, `path` | `v1`; `public_http` or `managed_service` |
| `phase`, `outcome`, `rejection_reason` | Last handler phase, final outcome and a bounded platform reason |
| `forward_attempts`, `replays` | Proxy dispatches; replays are attempts after the first |
| `retry_stop`, `limiter_scope` | First retry refusal; rejecting account/app/tenant/surface/consumer/rule allowance |
| `cache_outcome`, `circuit_verdict` | Cache serving decision; endpoint admission or refusal when consulted |
| `measured_phases` | Comma-separated policy/body/wake/capacity/backoff phases actually measured |
| `policy_read_ms`, `body_admission_ms`, `wake_admission_ms`, `capacity_wait_ms`, `retry_backoff_ms` | Local durations rounded down to milliseconds |

`surface` identifies pre-auth source guards; `consumer` identifies a dimensional
edge-rule allowance. These names describe the rejecting bucket family without
exporting the customer selector, consumer ID, source IP or rule content.
An empty limiter or retry reason means none was recorded. Cache starts at
`not_consulted`; circuit starts at `not_observed`. Public forwarding does not
invent a managed-service circuit verdict. Durations can overlap across phases;
overlapping work in the same phase is counted once. A zero duration alone does
not prove that a phase ran; consult `measured_phases`.

Outcomes are `edge_response`, `upstream_response`, `refused`, `deadline` and
`canceled`. `upstream_response` identifies the dispatched path and includes
errors emitted by a forwarding bridge. A guest HTTP 401/429/503 does not imply
a platform authentication/rate/capacity refusal. Cached origin errors and
configured fixed preview replies remain `edge_response`, including 404/410.
Before dispatch, a response
without a specific platform reason reports `<phase>_response`. Explicit deadline,
policy, rate-store and security reasons remain distinct. A successful stream
handshake can detach its admission deadline; a later expired handshake timer
does not relabel its outcome. Security revocation after headers still records
the lifetime reason even when a new error body cannot be sent.

The record contains 17 fixed scalar attributes, with closed vocabularies and
saturating counters, and seals at completion. Each service child owns its own
record; detached cache refreshes cannot revise the parent. No credential,
request content or error text enters these new fields. A dispatch count does
not establish guest execution, rollback or nested application work. These
local durations do not establish a latency SLA. Sampling and log levels still
apply, and customer-authored telemetry is not protected platform proof.
Managed realtime, TCP tunnels, detached jobs and direct bridge calls have their
separate owners; malformed service URLs rejected before dependency trace entry
do not receive this record. Complete-path and deployed acceptance remain pending.

## Failure and update behavior

A fresh production request requires authoritative reads even with a warm
compiled-policy cache. Store failure returns `traffic_policy_unavailable`/503
with `Retry-After: 1` before authentication, wake or guest execution. A compiled
policy with reported rule errors also refuses an unverified owner snapshot.
Failure checks run after owner resolution: another account's broken free-form
host rule or unavailable preset cannot take this tenant offline. A
verified empty policy is valid. Invalidation between selector-host and
base-host reads refuses the inconsistent combination. Normal convergence
fences new requests; previously admitted requests retain their inputs.

Account/app/deployment emergency cancellation now uses the separate durable
generation fence described in [HTTP security revocation](traffic-security-revocation.md).
Its local gateway and Postgres checks do not establish full path or deployed
acceptance. Imported document cache entries are owner scoped. An invalidation
during a pending document load refuses its unpublished snapshot, while an
already pinned request retains its contract. The configured total deadline
uses the trusted request start after app resolution, including time spent in
the bounded initial hostname/contract reads; expiry returns 504 once the app
budget is known. Complete-path timing/failure acceptance remains required.
Managed service calls now pin the policy inputs described below. Simulator/runtime
agreement and complete service-path acceptance still require verification.
Native lifecycle and deployment acceptance remain pending.

### Read-only deadline and retry preview

`gregale edge-rules trace` and the dashboard report the total-deadline
candidate independently of the execution-budget step. Selection uses the
original public path and ingress headers. A positive total deadline pins the
execution rule across rewrites; a total field on a rule matched only after a
rewrite does not start an ingress timer. Header actions can change an
execution override while selectors continue to use the original headers.
The candidate remains visible if a fixed response, cache lookup candidate or
another runtime-dependent gate ends the trace before execution.

JSON includes `simulation.total_deadline_policy` with the selected rule,
original selection path, configured/effective milliseconds, plan ceiling,
coverage and `enforcement_status: "unverified"`. Text and dashboard show the
same distinction. Configuration is insufficient to infer an available
gateway signing key, operator retry gate, shared-counter health or actual
admission. Cache, throttle, circuit and retry runtime outcomes remain
incomplete. A route to another app remains incomplete until that owner's
policy and plan can be resolved.

Preview compiles existing budget/retry rows with the runtime's resolver.
Invalid total deadlines surface the owner's host-snapshot verification
refusal, including when the invalid rule's path would not select this request.
Retry actions with fewer than two attempts are omitted from sequential
selection and cannot shadow a later compiled retry rule.
API writes still apply their normal defaults. A legacy stored zero retry floor
is shown as zero, while a zero low-traffic retry allowance uses the forwarding
loop's minimum at spend time. Execution overrides are clamped as integers
before duration conversion, including extremely large positive values.
POST/PATCH opt-in still requires a key; the app must honor it to prevent
duplicate side effects after a transport failure.

Local compiler/handler and CLI/dashboard checks cover these selectors and
numeric bounds. Fleet feature availability, policy publication across owners,
complete path/load/recovery evidence and native acceptance remain required.

## Public deployment routing

Before cache access or dispatch, production public HTTP routing reads app
ownership, scoped positive deployment weights and any release/revision/host pin
eligibility in one read-only repeatable-read Postgres transaction. The read has
a 250 ms bound and consumes the request's total deadline. This routing read is
required even with warm compiled host rules and cached VM targets: failure
returns `traffic_policy_unavailable`/503 before wake; total deadline expiry
returns 504. Invalid pin headers retain the existing 400/422 validation contract.

One deployment is selected for the request. Exact host/smoke pins take
precedence, followed by a verified project release, an eligible revision, then
version affinity or a valid instance preference within the positive roster.
Ordinary requests without a usable preference use weighted selection. A cold
selected deployment is woken directly. Instance rotation, capacity waits,
retries and detached cache refresh stay inside it. Fresh requests read changed
weights or release graphs. Cache keys include the selected deployment for both
keyed and ordinary traffic; old app-only entries are no longer used by this path.

The app retains one local cold-wake queue across cohorts. A request behind a
different cohort retries its own admission after that generation finishes;
its original total queue allowance spans both generations. The gateway-wide
admission queue still bounds scheduler work. Browser navigation retains its
retry page while the bounded detached wake continues. Ordinary burst expansion
retains one capacity worker per app and carries the admitted deployment on
every scheduler batch member. Every admission still passes scheduler capacity
and placement gates. Ordinary traffic uses the steady app/plan ceiling. A cold
second positive cohort beside a routable cohort may use the existing bounded
rollout overlap; explicit rollout verification retains its existing allowance.
The scheduler remains authoritative for whether that overlap is allowed.

The fingerprint includes the verified routing roster and pin verdicts. Selection
entropy and the chosen deployment are excluded. Request spans separately carry
`gregale.traffic.selected_deployment_id` and `gregale.traffic.deployment_selection`.
Expired release/revision pins retain 410; an incomplete public release retains
503. More than 100 positive deployments refuses verification without truncation.

These routing reads verify the resolved host's app and pinned deployment.
Cross-process exact-wake coalescing, complete synthetic admission/security
ownership and preview agreement remain pending.

## Managed service calls

Both internal service-proxy listeners read discovery, alias access and
authorization in one read-only repeatable-read Postgres transaction. This
includes preview/scenario-test namespace, caller binding and transport policy,
target caller/method/path grants, dependency reliability, and target protocol
and WebSocket posture. Release membership/expiry, exact override eligibility and
positive deployment weights use that same committed view. The complete lookup
has a 250 ms bound and releases the
transaction before queueing, wake or dispatch. Missing verification returns
503 with `Retry-After: 1`; it does not use a warm authorization fallback.

The gateway copies the result and retains it across wake and retry. The
response header and dependency span carry a fingerprint that also includes the
effective default retry configuration and named service/alias. Declared call
timeouts count from service-handler entry, so policy lookup consumes that
budget. Application headers and trailers cannot replace the proof. Raw Upgrade
bytes have span evidence but no protected HTTP proof header.

Emergency account/app/deployment generations remain fresh independent checks.
Endpoint health remains live. The snapshot chooses one deployment before wake:
release membership takes precedence over an exact override, followed by version
affinity or weighted selection. Ordinary calls without an affinity key use local
random selection against the pinned weights. Instance rotation/local-node
preference and retries stay inside that deployment, including after a cold wake.
A newly published graph or changed weight cannot redirect an admitted call.
No eligible deployment returns 503 before wake. The roster is bounded to 100
positive deployments; a larger roster refuses verification.

The policy proof includes verified release/override verdicts and weights.
Selection entropy and the individual random choice are excluded, so equivalent
policy inputs keep the same fingerprint. The dependency span separately records
`gregale.traffic.selected_deployment_id`. Expired release pins return 410,
ambiguous membership/conflicting pins 409, invalid header shapes 400, an explicit
release without verified source deployment 403, and an ineligible exact override
422. These refusals precede wake/guest dispatch. Normal updates retain admitted
policy; emergency generation fences still apply.

## Covered paths

| Path | Snapshot and evidence |
| --- | --- |
| Ordinary public HTTP dispatch and response cache | Compiled host rules, imported/scoped route contract, resolved app flags/plan and atomic scoped deployment routing; one deployment through wake/retry/refresh; response/span/log fingerprint |
| Public edge answers without guest dispatch | Compiled host rules, imported/scoped route contract, resolved app flags and plan; unused deployment routing is excluded |
| Named environment, verified domain, listener selector | Fresh route graphs; selector/base owned rules and referenced presets share the app transaction; stable environment URL overlay included |
| Public streaming and raw Upgrade | Pinned rules/app inputs; span/log evidence; ordinary HTTP header only where normal response commitment is used |
| Managed service proxy | Atomic access/namespace/reliability/transport/release/override/weight snapshot; one deployment across wake/retry; response/span fingerprint |
| Synthetic HTTP through the public routing handler | Same handler snapshot; direct bridge dispatch has no host-policy snapshot |
| TCP service tunnels, detached jobs, unmediated guest sockets | Outside this public HTTP snapshot |

## Local evidence

Full gateway and internal-gateway command suites passed on Darwin. Tests cover
an actual handler wake while app settings and rules change, route/retry/deadline
matching after refresh/store failure, resolved preset changes, deep copying,
selector-host generation changes, compile refusal and protected response
evidence. A representative snapshot freeze benchmark measured 22.4 microseconds
and 12.1 KB per request on the local M3 Pro; this is not deployed load evidence.
These checks do not establish native or deployed acceptance.

## Tenant binding changes

Hostname creation, verification and removal, surface status changes and linking,
tenant reactivation, bulk onboarding, reconciliation and offboarding validate
both tenant-enabled and tenant-disabled routing before publishing intent. This
includes overlapping custom-domain owners and global-route owners. A pending
hostname or a hostname on a soft-deleted surface still reserves global routing
until its row is removed. Active verified public tenant surfaces precede domain
fallback; a suspended tenant blocks it while retaining its claims. Platform
namespaces retain precedence.

A deletion or suspension can expose an oversized domain policy owned by another
account. These changes return the existing `traffic_policy_too_large` or
`traffic_policy_too_complex` problem with HTTP 422. The response gives the cap
and a proven lower bound, excluding foreign hostnames, policy scopes and exact
counts. Repair the affected policy and retry; an operator may be needed for
another account's policy. Refused bulk operations preserve links, credentials,
webhooks and receipts. Dry runs and reconciliation plans validate the complete
proposed topology without publishing it. The HTTP surface deletion cascade
removes the surface and hostname rows atomically.

Immediate platform-tenant suspension remains available independently of
cleanup. It retains claims and blocks dispatch. Offboarding additionally
releases managed claims and can refuse an unsafe fallback; repair that policy
before retrying cleanup.

PostgreSQL transactions discover owners, acquire the global routing lock and
sorted account locks before a repeatable-read snapshot, and repeat discovery
before writing. Lock retries release the connection between attempts.
Verification binds the observed challenge and hostname row; removal binds the
HTTP-authorized surface and original row. The memory store stages and validates
its final topology before publishing maps or side effects.

This coordination is local software evidence. App/alias/revision/operator binding
transitions, complete preview/runtime/path agreement, real daemon fleet load and
recovery, customer/staging acceptance, and native VM/firewall/leak acceptance
remain release gates. No dedicated Linux x86_64 KVM acceptance host is currently
available.

## App binding changes

App visibility and deleted status change tenant-hostname eligibility. Such a
withdrawal can expose a custom domain owned by another account. Visibility/status updates,
deleted-status CAS, restoration, rename, scheduled deletion, cascade deletion and
physical purge use coordinated before/after analysis for both tenant routing
modes. Account domains and tenant hostnames participate in owner discovery;
purge also includes legacy domains whose redirect target is the app being removed.

A refused app binding change returns the existing 422 traffic-policy problem.
The response reports a proven lower bound (`observed = limit + 1`) and omits the
foreign witness hostname, exact count and policy scope. Repair the affected
policy and retry; an operator may need to repair another account's policy.

Deletion refusal preserves the app row, grace deadline, runnable invocations,
reserved async quota, command tasks, crons, deployments, builds, cleanup handoffs
and activity records. Accepted scheduled or cascade deletion cancels eligible
invocations and releases their reserved quota in the same transaction as the app
change. Managed invocations already dispatching keep their existing cancellation
fence. Audit and routing notifications follow successful commit.

Physical purge analyzes removal of domain and tenant reservations before deleting
children or the deletion claim. The memory implementation also clears default
domain selections, so a later foreign claim cannot inherit a deleted selection.
A soft-deleted or pending hostname can still be a routing reservation until its
row is removed.

Project reconciliation and preview set replacement validate their combined final
app topology through the same binding guard. A refused batch preserves its apps,
leases, tombstone slugs, crons, project metadata, cleanup work and preview receipt.
An intermediate deletion followed by restoration in the same batch is evaluated
as the final restored binding. Memory batches stage changes before publication;
native batches keep their existing cleanup inside the transaction. Preview
retirement still hands resource cleanup to the preview janitor.

Physical account retirement commits its complete routing cascade through the
same binding guard. Discovery includes foreign surfaces linked to retiring apps
and legacy domains that redirect to those apps. Refusal preserves the pending
account, routing reservations, secrets, API keys, queued work, quota and deletion
audit; restoring the pending account remains available after a refused sweep.
Repair the affected policy before retrying retirement.

Accepted retirement removes owned invocations and invocations referencing the
retiring apps, refunds their reserved async slots, and commits child cleanup and
the deletion audit together. This physical cleanup does not prove cancellation
or rollback of application side effects. Captured app guards recheck customer
ownership under an app row lock and refuse a changed owner before mutation.
Node reassignment changes placement and does not transfer customer ownership.

The remaining operator and alias/revision writer and resolver coverage needs
complete-path qualification. VM, firewall, restore,
process-death, leak, real fleet/load/recovery and staging acceptance remain
pending. No native KVM acceptance host is currently available.


## Alias reservations and removal

A stored deployment alias reserves its hostname independently from its target's
current ability to serve. Failed or cancelled deployments, cleared targets and
deleted or internal owners return a routing miss while the alias mapping remains.
Terminal pipeline status changes can still be recorded. A genuinely absent alias
retains the legacy primary-slug fallback.

Removing an alias, permanently purging its app or retiring its account can expose
a primary hostname owned by another account. These removals use the shared binding
guard before publication. Discovery includes raw alias reservations and potential
`tag-` primary slugs within the existing metadata/input caps. A newly exposed
primary has no legacy overload allowance. A refused removal retains its mapping,
owner and cleanup intent; repair the affected policy and retry. The existing 422
traffic-policy problem reports the cap and a proven lower bound without revealing
another account's witness hostname, policy scope or exact count.

Native alias writes repeat captured ownership under the app row lock and commit
through the shared binding transaction. Memory writes validate the proposed
mapping before changing it. Public resolution reads raw reservation state in the
same PostgreSQL policy snapshot and includes that result in its fingerprint.
Failure to read the reservation refuses fallback. Dispatch verification rejects
an alias projection whose mapping or serving eligibility changed before dispatch.

Local memory and PostgreSQL tests cover these transitions, safe primary fallback
after removal, stale dispatch refusal, reservation-reader failure and transaction
release. Injected reader failure does not establish real fleet/store outage or
recovery acceptance. Complete writer/resolver coverage and path
agreement, native VM/network/leak checks, fleet/load/recovery and staging
qualification remain pending.

## Immutable deployment revision publication

The aggregate guard includes canonical
`deploy-{positive stored revision}-{slug}.gregale.dev` URLs on eligible public
apps. The revision namespace uses the deployment suffix independently from the
primary/alias apps domain; setting the apps domain to empty does not disable
revision URLs. Pending, building, imaging, snapshotting and live targets with no
deletion timestamp participate. Superseded, failed, cancelled and deleted targets,
and deleted or internal owners, do not supply a serving allowance. Legacy rows
with revision zero do not receive a rank-based revision URL.

Revision URLs retain ordinary account-wide matching rules and referenced presets.
Deployment creation, including creation with activity, validates the new revision
and any pending predecessor supersede before committing either. A new hostname
must fit the platform allowance even when its selectors predate the URL. Positive
status changes, mark-live, app restoration, public visibility and rename validate
the same projection. An unchanged eligible URL retains incremental policy repair.
Dark promotion keeps its existing pending, explicit-zero-traffic eligibility
requirements and uses the shared guard.

Native mutations repeat captured app ownership and deployment membership under
the account, app and deployment locks. Memory mutations check the complete staged
proposal before publishing rows or activity. Alias revival also applies raw alias
precedence: a primary hostname hidden by an alias reservation cannot grant the
alias a legacy policy allowance. Refusal preserves rows and side effects; repair
the policy and retry. Creation returns the existing structured 422 traffic-policy
problem, including the cap and a proven lower bound while withholding foreign
witnesses, scopes and exact counts.

The hostname writer and runtime parser share the existing grammar. An unavailable
canonical revision URL stays reserved, cannot substitute a synthetic route or
fall through to a primary hostname, and invalidates a previously resolved dispatch
projection. Local PostgreSQL tests exercise these cases without notifications
and verify transaction release. They do not establish deployed fleet, load,
outage/recovery, staging or native VM/network/leak acceptance.

Ordinary response decision evidence uses the budget's wall-clock expiry even
when transport completion precedes its context timer. A successfully detached
stream retains its independent lifetime reason.
