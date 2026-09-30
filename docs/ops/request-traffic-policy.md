# Request traffic policy snapshots

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
registration and environment clones share account serialization. A session
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
Analysis also limits inputs to 100,000 groups/assets/environment identities, metadata to 64 MiB,
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
primary-hostname and alias/domain activation remain pending.

Route creates/updates also check enabled route-only discovery across accounts
using the same per-host language and resource ceilings. Disjoint hosts retain
separate allowances; a connecting wildcard does not turn their union into a
flat global quota. Both owned and global before/after reads use the stable
transaction view, so concurrent deletion cannot hide an increase above a
legacy overload. Global refusals use `global_route_` scopes and the same
structured 422 codes. Unsupported legacy route shapes refuse global route
writes until replaced, disabled or deleted; non-route writes in other accounts
retain their owned scope. No action bodies are transferred for this analysis.
This initial guard includes the full selector language conservatively. Actual
claimed/reserved-host exclusions and newly unclaimed scope transitions still
need integration with hostname binding projection.

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

MemStore also checks route-only global aggregates under that mutex.
Primary-hostname and alias/domain activation, complete global binding/synthetic
path agreement and recovery acceptance remain rollout requirements. These
write checks do not establish release acceptance.

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
ownership, bounded decision evidence and preview agreement remain pending.

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
