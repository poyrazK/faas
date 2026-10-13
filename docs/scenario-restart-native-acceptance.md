# Native checkout and recovery acceptance

`make native-scenario-restart-acceptance` builds the candidate Gregale CLI from
the current working tree and runs checkout and recovery probes against a
designated native fleet. It does not deploy platform daemons, change fleet
configuration, or change existing applications. All mutations belong to
newly-created developer workspaces for the dedicated acceptance account.

## Runner and fleet prerequisites

- Native x86_64 Linux with accessible `/dev/kvm`, Go from `go.mod`, Node 22+,
  Python 3, OpenSSL, and `flock`.
- Root-owned regular files `/etc/faas/scenario-restart-acceptance-host` and
  `/etc/faas/scenario-restart-acceptance-platform-sha`, neither writable by group
  or others. Release tooling must put the fleet's installed 40-character commit
  SHA in the latter. The marker designates a native runner; nested virtualization
  is not supported acceptance infrastructure.
- A dedicated fleet with private TCP routing and TCP fault injection enabled,
  compatible fresh-restart and wake-timeline APIs, and capacity for both test
  workloads, 18 simultaneous checkout requests for the batch, and three
  simultaneous HTTP probes for recovery. The harness does not enable those
  settings.
- `FAAS_API`: dedicated public HTTPS API origin, without a path or credentials.
- `FAAS_TOKEN`: acceptance account token supplied through the secure environment.
- `FAAS_SCENARIO_ACCEPTANCE_ACCOUNT`: expected canonical account UUID. The
  harness verifies `/v1/account` before provisioning so an unintended account
  cannot be used silently.
- `GREGALE_DEPENDENCY_DATABASE_URL`: restricted runtime connection for a migrated
  operator-owned disposable Postgres fixture, reachable from this runner and
  the payment VM with verified TLS. Serving needs DML on the two write ledger
  tables; migration uses a separate credential before qualification. The URL must
  use a DNS hostname and explicit `sslmode=require` or `verify-full`, with a
  publicly trusted server certificate. The DNS target must be reachable under
  the existing guest egress policy; no routing or firewall policy is changed.
- `GREGALE_DEPENDENCY_DATABASE_DISPOSABLE=1`: explicit fixture opt-in. The
  automatic developer `postgres: true` binding is runtime-only and does not
  create the application schema. No migration credential is sent to payment.
- Optional `FAAS_SCENARIO_ACCEPTANCE_PROJECT`: isolated project prefix, 3–20
  slug characters; defaults to `native-checkout`.

Use the existing host lock `/var/lock/faas-builder-acceptance.lock`; runner
administration must grant the acceptance user access. This serializes the run
with native builder/e2e drills. The checked-in manual workflow has a 800-minute
outer timeout for all thirty-eight cases and their independent cleanup allowances.
It targets
`gregale-scenario-acceptance` self-hosted runners and the `scenario-acceptance`
GitHub environment. Configure its API/account variables and token secret before
dispatch. The workflow remains inactive until these local changes are published.

## Run

With credentials already injected into the environment:

```sh
make native-scenario-restart-acceptance
```

For an alternative Go binary, set `GO=/absolute/path/to/go`. Evidence is written
into a new private directory under `scenario-restart-native-evidence/`; override
the parent with `FAAS_SCENARIO_EVIDENCE_ROOT`. Each invocation uses fresh outputs,
never a saved report as proof of a new execution.

The command validates and preflights thirty-eight generated manifests using absolute
paths to the current starter sources. Each uses a different project name and
Gregale creates a new isolated workspace/run ID:

1. **Success:** confirm baseline checkout; lose payment acknowledgements after
   a simulated charge is committed; prove positive fault matches; cold-replace
   checkout; verify its persisted ledger identity and pending intent; explicitly
   clear the fault; recover with exactly two total charges and two total orders
   (one baseline, one recovered), including duplicate requests and load gates.
2. **Intentional failure:** complete the charged-outage assertions, then execute
   a named step that exits 42 while the fault remains active. A different failure
   or missing stage cannot satisfy this case.
3. **Cancellation:** complete charged-outage assertions and enter a named waiting
   step. That command emits a one-time nonce, then the harness sends SIGINT to
   Gregale. The resulting receipt must show cancellation at that exact step.
   The harness keeps reading logs while Gregale performs independent cleanup.
4. **Batch success:** six distinct customer orders, three simultaneous retries
   per key, are charged once each while acknowledgements are lost. All six
   payloads and pending states must appear in named HTTP assertions before and
   after cold checkout replacement. Recovery must confirm those exact payloads
   with seven total charges/orders including the baseline. Command assertions
   reject changed-payload retries with 409 and prove confirmed replays add no
   provider attempts or charges.
5. **Batch intentional failure:** all six charged, pending orders are verified,
   then the exit-42 step fails while the fault remains active.
6. **Batch cancellation:** the same charged batch reaches the nonce waiting
   step, then receives SIGINT. Both negative batch cases must independently
   prove cleanup of their two developer workspaces and fault namespace.
7. **Pending healthy control:** a separate acceptance-only Node fixture runs
   independent fast dependency and delayed HTTP streams through baseline, a
   positively matched TCP acknowledgement outage, and recovery. Its 25ms delay
   must recover within two seconds and remain healthy for two seconds, using
   one-second windows, p95 ≤1500ms, error rate zero, at least three completed
   samples per window, and maximum pending age 500ms. A final JSON assertion
   proves the TCP dependency is reachable again.
8. **Pending stuck request:** the same fixture holds requests for 15 seconds.
   A request started before fault clear stays in flight through the recovery
   deadline while the fast stream remains healthy. Gregale must return a failed
   scenario specifically because `max_pending_age` is exceeded. Positive fault
   matches, both streams, evaluated windows, healthy completed-sample budgets,
   overdue request counts, and the specific failure reason are mandatory. An
   unrelated API, HTTP, latency, or sample-budget failure cannot satisfy this
   negative case. Cleanup is still mandatory.
9. **Before acceptance:** the provider has received the write but holds it
   before creating a durable business intent. The checkpoint must prove
   `accepted: false`, no pending intent, no new charge, and an active hold.
10. **Pending intent:** the provider has durably bound the payload and holds
    before commit. The checkpoint must prove one pending intent and an active
    hold, with the baseline charge preserved.
11. **Committed without acknowledgement:** the provider has durably committed
    the new charge and holds its acknowledgement. The checkpoint must prove
    the second charge is present, no pending intent, and an active hold.

12. **Dependency pending intent:** replace payment while its payload-bound
    intent is pending in external Postgres. Readiness must prove the database
    is healthy and its old in-memory hold is gone; the ledger identity and
    pending record must survive before/after replacement. Replay commits once
    and conflicting payloads remain rejected.
13. **Dependency committed result:** replace payment after its Postgres commit
    but before acknowledgement. The committed row must survive; concurrent
    same-key replay and conflicts cannot create another charge.

14. **Deployed TLS authentication stall:** a read-only application readiness probe
    calls payment inside the deployed workload. An opt-in payment fixture runs
    the fault proxy on its own loopback interface and reaches external PostgreSQL
    over verified TLS. Baseline returns 42; three explicit acquisitions time out
    while the initial authentication challenge is held; clearing the fault permits
    two successful readiness reads. No provider wake/restart is allowed. The
    application assertion command owns these phases and writes `auth-proof.json`,
    bound to the run ID and one stable proxy instance. The native verifier requires
    positive phase/timing evidence, exact attempt counts, verified TLS, empty pools
    and connections, and independent backend retirement after each phase. A baseline PID inventory and a no-new-client-backends
    check also cover processes that did not finish login.

15. **Deployed lost COMMIT acknowledgement:** the existing write caller and
    Postgres ledger run in real VMs. An explicitly armed loopback TLS proxy drops
    the successful COMMIT reply. The caller returns 503 with unknown outcome and
    exactly one provider attempt. Independent SQL must already show one confirmed,
    payload-bound charge. Explicit same-key replay confirms that charge; a changed
    payload returns 409. Counts remain one charge and zero pending rows. Cleanup
    stops the fixture, removes the exact run namespace, and independently proves
    backend retirement and namespace removal. A separate `commit-proof.json`
    records the positive response-drop evidence and stable provider/ledger IDs.

16. **COMMIT request exceeds caller deadline:** hold the outbound COMMIT for five
    seconds while the caller has a 1.5-second deadline. The caller reports unknown
    outcome. After query/connection retirement, independent SQL must show no
    charge, and the proxy must show zero forwarded commits. Explicit replay creates
    exactly one matching confirmed charge; changed-payload reuse returns 409.
17. **COMMIT reply exceeds caller deadline:** hold the successful reply for five
    seconds with the same caller deadline. Independent SQL must show the charge
    already committed, and explicit replay preserves that single charge. Both cases
    require one canceled hold, no automatic initial retry, joined work, no pool or
    timer resources, and namespace/backend cleanup.

18. **Deployed bandwidth restriction:** read a fixed 64 KiB PostgreSQL result
    through the provider's verified loopback TLS proxy. At 256 KiB/s the complete
    read returns its exact size and hash; at 16 KiB/s a 1.5-second query deadline
    rejects the partial transfer with 503. Clearing the restriction restores a
    complete read. Require positive pacing and canceled-transfer evidence,
    exact attempts/TLS counts, stable identity, joined buffers/connections/timers,
    and independent backend retirement after every phase.

19. **Deployed read interruption:** cut a 64 KiB result DataRow after a 2048-byte
    prefix. Require 503, a complete hash-matched read on a fresh connection, then
    two concurrent independent acquisitions with exactly one failed and one
    complete read under a second one-shot cut. A final fresh read must succeed.
    Require two positive cuts, one completed two-client rendezvous, exact attempts
    and TLS counts, stable identity, joined connections/buffers/timers, and
    independent backend retirement after every phase.

20. **Deployed SSLRequest stall:** withhold the initial SSL response for five
    seconds, before TLS or upstream login. Three explicit retries must each fail
    within the existing two-second connection deadline. Then three concurrent
    requests share a one-client pool; require observed queue depth two, bounded
    connection attempts, three failed receipts, and joined acquisitions. Clearing
    the fault permits two fresh verified TLS queries. Require stable identity,
    exact phase counters, canceled holds, and independent backend retirement.

21. **Shared application retry budget:** six concurrent callers share two extra
    read attempts in one caller process. A cancellation before retry admission
    consumes no token. Under the SSLRequest stall, six originals plus two retries
    must yield eight dependency attempts, with a maximum of two per request and
    bounded overall deadlines. Recovery succeeds on the first attempt despite the
    exhausted budget. Require caller/provider identities, an unchanged budget window,
    exact counters, queue evidence, joined deadlines/backoff, and backend retirement.

22. **Application circuit breaker:** two failures open the circuit. Six concurrent
    open-state requests fail fast without dependency attempts. After the cooldown,
    one half-open probe is admitted while four additional requests are blocked.
    A failed probe reopens the circuit; a canceled probe releases its slot and
    reopens conservatively. Removing the fault permits a successful probe to close
    the circuit, followed by normal traffic. Require state/epoch transitions,
    independent attempt counts, single-probe evidence and joined cleanup.

23. **Application bulkhead:** two active dependency calls and two queue slots
    isolate a stalled dependency from a healthy route. Cancel a queued request,
    then send six requests: two fail at the dependency, two expire in the bounded
    queue, and two are rejected immediately. Three healthy probes must respond
    while both dependency permits are occupied. Clearing the fault restores two
    successful reads. Require exact permit/queue counters and joined cleanup.

24. **Combined application policies:** shared retry budget, circuit breaker and
    bulkhead run together. Cancel two active requests and one queued request
    without spending retry tokens. A six-request failure wave must produce two
    rejected callers, two queue timeouts and two callers making one protected
    retry each. Four failed dependency attempts open the circuit. Open callers
    and probe-busy callers cannot reach the dependency. Failed/canceled probes
    reopen; successful recovery closes the circuit with the budget exhausted.

25. **Repeated outage and recovery:** three cycles use real 30-second retry
    windows, refuse a retry before expiry, recover and retire resources each time.
26. **Dependency isolation:** one caller has two providers and independent
    policies. Stall each provider in turn; six requests to its route saturate the
    bulkhead while six requests to the other route complete below 750 ms each.
    Require overlap, bounded retries, six fail-fast circuit rejections, three more
    healthy reads and successful half-open recovery. Retire all three workloads.

All three cases use the current `http-restart-matrix` source manifest, isolated
as one-case suites for native qualification. Each requires both caller-active
and named provider checkpoint assertions while the original request is pending
at submission; a completed cold replacement; idle readiness; preserved external
ledger identity and expected counts before/after; joined request cleanup; and
reconciliation to exactly one new charge with conflict rejection. The receipt
distinguishes an ended request from runner cancellation after replacement.
The complete user-facing matrix suite writes combined JSON/HTML/JUnit reports
and continues after individual case failure unless `--fail-fast` is selected.

The pending fixture is packaged independently in `native-probe`; it does not
add test endpoints to checkout or alter checkout's payment deadlines. These
fixtures prove HTTP client observations, not background-job counts. The original checkout
starter replaces its caller after timeout responses; the HTTP write case
replaces it while its request is pending. The portable batch test
separately exercises SIGKILL while checkout requests are in flight.

Each command has a 12-minute harness deadline; interruption is followed by a
separate four-minute cleanup allowance. Intentional negative cases return a
nonzero CLI status, which is expected only when their specific failure is proven.
The complete acceptance command exits zero only after all thirty-eight cases pass their
evidence checks. Missing hardware, credentials, capacity, reports, or skipped
stages fail the command rather than producing a green result.

## Cleanup and evidence

After each case, the harness polls the authenticated API for up to three minutes:
all exact developer sessions must return 404 (three for dependency isolation), the run's fault namespace must
return 404, and a complete paginated live-instance inventory must contain no
instances for all recorded app IDs. The account-wide instance query includes
deleted apps, so a tombstoned app cannot hide a still-live instance. Authorization
errors and incomplete/repeating pagination fail verification.

For dependency cases, an independent Node/Postgres read also requires both
ledger tables to contain no rows for the exact recorded run namespace. This
probe runs even when receipt validation fails. The fixture cleanup uses its
restricted runtime connection and deletes only that namespace. A failed or
unavailable database probe cannot qualify the gate.

This proves API retirement and absence from live VM inventory. It does not
replace native host resource-journal/leak checks for orphaned processes, netns,
TAPs, cgroups, or filesystem remnants. Run the fleet's existing leakcheck gates
when qualifying host resource cleanup. The harness does not run host-wide
destructive cleanup or modify unrelated tenant resources.

Artifacts include each manifest, command log, JSON/HTML/JUnit report, and the
combined `verdict.json`. JUnit must contain the same receipt as JSON, without
skips. The verdict records run IDs, API cleanup observations, manifest/report
digests, candidate CLI digest, Git HEAD and dirty-tree state, and the installed
platform release stamp. CLI and platform revisions are recorded separately:
this acceptance adds CLI behavior using existing platform APIs and does not
silently claim the fleet runs uncommitted local platform changes.

`runner.json` records a blocked prerequisite check or readiness. It is not an
acceptance pass. A passing version-20 `verdict.json` requires all thirty-eight executed
cases, each with
`verified: true` and successful independent cleanup. A saved version-14, version-13, version-12, version-11, version-10, version-9, version-8, version-7, version-6, version-5, version-4, version-3,
version-2, or three-case verdict does not qualify the expanded gate. Receipt validation failures also trigger
independent cleanup inspection when the created workload identities are valid.

## Portable harness checks

```sh
make test-scenario-restart-acceptance
```

These test manifest generation, receipt rejection, nonce-triggered cancellation,
matching artifacts, hardware admission, and API cleanup/pagination using local
fixtures, the complete Node acceptance suite, matrix scheduling/reports, and all twenty manifests through
Gregale's parser and source validator. The harness contracts are also invoked by
the Gregale package's portable tests. They never qualify a native fleet. The starter's Node tests separately exercise
process crashes and external-ledger replacement without KVM.


`make test-scenario-dependency-postgres` separately runs the real Postgres 16
portable acceptance gate in a disposable local Docker container. It tests role
separation, two dependency SIGKILL checkpoints, concurrent writers/replay,
namespace isolation, database/COMMIT failures, redaction, and container removal.
It does not qualify a deployed fleet or change external databases.

## Native authentication fixture and trust

The fourteenth case uses `gregale-native-auth-test.yaml`, `native-auth-app`, and
an explicitly selected `payment-postgres` fixture mode. Ordinary payment serving
keeps its existing startup and ledger behavior. Shared protocol helpers are
packaged under `payment-postgres/native-fixture`; portable test entrypoints
re-export these same implementations. The native provider exposes only a
read-only SELECT readiness probe, redacted status, and token-protected control.
It does not initialize or mutate write-ledger rows. Every successful acquisition
is retired, so the next probe necessarily exercises fresh authentication.

For this case the runner generates a fresh one-day loopback TLS identity and a
64-character control token in a private temporary directory outside artifacts.
Only sealed workload secrets carry its certificate/key/token into the new VM;
no command arguments, manifests, logs or reports contain their values. The
provider trusts that certificate only for its local pool connection, and verifies
the upstream database using normal public roots and the configured DNS name.
The listener stays on guest loopback; no database port is exposed through HTTP
routing. Remote upstream use is an explicit verified-TLS fixture option. The
host key files are removed even on CLI failure; deleting the developer workspace
retires guest secret bindings and processes.

Authentication control requires a bearer token and exact run namespace. Its
fixed five-second fault bound exceeds the provider's normal two-second connection
deadline. The caller performs one request with a four-second deadline and returns
a fixed dependency-unavailable receipt. Clearing a fault never weakens password
or certificate verification. Cleanup stops the pool/proxy before normal CLI API
retirement; an independent runtime-credential SQL read then verifies no sessions
for `gregale-native-auth-<run-id>`, and compares client-backend PIDs with a
pre-fault baseline to detect incomplete-login processes too. Use a dedicated
fixture without unrelated new database clients during this case; the probe
fails on unexpected new processes and never terminates them. The gate requires this check even when report
validation fails. The final verdict includes the authentication proof digest.

The portable Postgres gate tests this exact caller/provider/driver lifecycle
locally, including unauthorized control rejection and evidence redaction. That
pass is not native qualification. A version-20 blocked `runner.json` or portable
proof cannot replace all twenty real VM cases on the designated KVM fleet.


## Native COMMIT acknowledgement fixture

The fifteenth case uses `gregale-native-commit-test.yaml` and the existing `app`
and `payment-postgres` sources. It reuses the authentication case's short-lived
loopback TLS identity and bearer control token, scoped to this invocation. Its
upstream TLS verifies public trust roots and the external database DNS name.
`GREGALE_NATIVE_COMMIT_LOSS=1` and the disposable database opt-in are both required.
The provider starts the existing migrated, runtime-only ledger through the proxy;
no migration credential or schema fallback is introduced.

The proxy is armed only after startup and before the single ambiguous write. It
withholds successful PostgreSQL COMMIT completion and idle ReadyForQuery frames;
SQL errors cannot create positive acknowledgement-loss evidence. The proof has no
SQL, payloads, passwords, private keys, or bearer tokens. The host driver checks
state directly through a separate PostgreSQL connection before replay, after
replay/conflict, and after cleanup. The native verifier requires the dropped reply,
one initial attempt, exactly two forwarded commits overall, one persisted matching
charge, unchanged ledger/provider identities, joined resources, and independent
namespace/backend cleanup. As with authentication qualification, use a dedicated
fixture with no unrelated new client sessions during the case. Portable PostgreSQL
validation does not qualify the designated KVM fleet.


## COMMIT deadline qualification

Cases 16 and 17 use `gregale-native-commit-request-test.yaml` and
`gregale-native-commit-response-test.yaml`. Their sealed fixture mode fixes one
five-second request or response stall; bearer controls cannot select arbitrary
plans. The existing caller's configured 1.5-second deadline ends the HTTP request.
Database work is subsequently bounded by the existing pool/query and transaction
idle deadlines; the harness joins that work before observing or replaying it.
Cancellation here proves eventual database connection retirement, not immediate
propagation of the caller's abort into PostgreSQL.

The proof binds the mode, namespace and stable provider/ledger identities. It
requires a caller response between 1 and 3.5 seconds, a canceled COMMIT hold between
3 and 5 seconds, and exact forwarded-commit counts. Request hold requires zero rows
before replay and one afterwards; response hold requires one in both observations.
The host verifier rejects an expired hold, wrong direction, missing timing, automatic
retry, premature replay, residual resources, or incomplete database cleanup. All
thirty-eight cases are required by verdict version 20; earlier verdicts do not qualify.


## Bandwidth qualification

Case 18 uses `gregale-native-bandwidth-test.yaml` with the existing read caller
and an explicitly opted-in mode of the authentication provider fixture. No ledger
rows or schema changes are required: a fixed SQL read produces one 64 KiB row.
The provider releases every acquisition and returns only byte length and SHA-256
once the entire query completes. A partial result produces the fixed retryable
503 receipt, without payload or database error disclosure.

Namespace-bound bearer controls select fixed response rates (262144 or 16384
bytes/s), clear the limit, or stop the fixture. Rates apply to aggregate PostgreSQL
protocol plaintext bytes written by the proxy, including login/result frames;
they are not encrypted wire throughput or proof of peer delivery. The native
verifier requires a successful complete/hash-matched limited read, a slower
canceled transfer that exceeded the 1.5-second query deadline, and complete
recovery after clear. `bandwidth-proof.json` binds all evidence to the run and
one stable provider instance. It excludes SQL, row content, keys and credentials.

Independent host probes require no remaining named or new backend sessions;
as with other TLS fixtures, the external database must be dedicated during the
PID-baseline interval. Version 20 requires all thirty-eight cases. Portable PostgreSQL
validation exercises the exact sources and proof validator, but native KVM
qualification remains a separate prerequisite.


## Read-interruption qualification

Case 19 uses `gregale-native-read-cut-test.yaml`, the existing read caller, and
an opt-in read-cut provider mode. The same fixed 64 KiB row is checked by byte
count and SHA-256 only after a complete query. Controls arm a fixed 2048-byte
DataRow cut, arm one cut with a concurrent pair, clear, or stop; arbitrary frame
offsets and payloads cannot be supplied through bearer controls.

The fixture permits two pool clients in this mode. The concurrent control creates
a bounded rendezvous after each client has completed verified TLS/login and pool
acquisition. Both queries start only once the pair has arrived; a missing participant
fails closed after two seconds, releases its client, and leaves no rendezvous timer.
This verifies two independent connections rather than serialized requests through
one pooled client. The one-shot cut must affect only one query; the other returns
its complete result. The host proof verifier requires the rendezvous receipt,
exact attempts/TLS counts, one 503 and one hash-matched 200 response, two positive
cut records, and a final successful fresh read without another fault.

`read-cut-proof.json` binds evidence to one run/instance and contains no SQL, row
payloads or credentials. Cut records require a 2048-byte prefix locally flushed
from a 65547-byte DataRow frame. This proves local flush completion, not peer
receipt of exactly that prefix; the receiving portable protocol tests establish
prefix delivery separately. Independent backend retirement and normal native
workload cleanup remain required. Version 20 requires all thirty-eight cases on the
designated KVM fleet; portable qualification does not replace those runs.


## Connection-establishment qualification

Case 20 uses `gregale-native-ssl-request-test.yaml` with the existing read caller
and an opted-in SSLRequest provider mode. Its fixed five-second client-leg hold
prevents the server's initial SSL acknowledgement before TLS negotiation and
upstream connection establishment. Existing two-second pool connection/acquisition
deadlines bound callers; no fallback to plaintext is permitted.

The assertion driver performs three sequential explicit retries, then three
concurrent requests sharing a one-client pool. Provider evidence must show queue
depth two and exactly seven acquisitions including baseline. Queued requests may
expire before starting a connection, so the verifier bounds failed connection
attempts to four through six overall, while requiring one SSLRequest stall record
per failed connection. All holds must be canceled before their five-second timer
expires. Verified TLS/login counts remain at baseline throughout the fault.

After clearing the fault, two fresh successful SELECT 42 queries raise acquisition
count to nine and verified TLS/login counts to three. Cleanup and each phase must
join sockets, negotiations, pool waiters and timers; independent host probes confirm
backend retirement. `ssl-request-proof.json` binds receipts, queue depth and positive
phase evidence to one namespace/instance without credentials, SQL or auth payloads.
The gate requires all thirty-eight cases and version 20 on the designated KVM fleet;
portable PostgreSQL checks do not establish native qualification.


## Shared application retry-budget qualification

Case 21 uses `gregale-native-retry-budget-test.yaml`, `native-retry-app`, and the
existing SSLRequest provider. One caller process owns two read retry tokens per
30-second monotonic window; requests may make at most two attempts, with a
1.5-second attempt deadline, a 100 ms cancelable delay, and a 4.5-second overall
deadline. Originals never consume retry tokens. The window opens on first retry
admission, expires lazily, and has no replenishment timer. Admission occurs
synchronously, so concurrent requests share one allowance within this process.
Tokens reserved before a canceled backoff remain spent, conservatively bounding
attempts; cancellation before admission consumes none.

The command cancels a request while its first dependency attempt is active,
requires no retry admission, then sends six concurrent failing requests. Exactly
two requests may retry, so per-request attempt counts are 1,1,1,1,2,2 and the
provider must observe eight attempts for the cohort. Positive pre-TLS fault and
queue evidence proves failures reached the dependency; server counters protect
against fabricated attempt receipts. Caller timers are explicitly cleared and
requests joined. Database work is subsequently bounded by the provider pool's
existing deadlines before independent backend retirement is checked.

Clearing the dependency fault permits two first-attempt successes with the budget
still exhausted. Caller and provider identities, window ID, budget configuration,
exact aggregate counters and independent cleanup are bound in
`retry-budget-proof.json`, without credentials or SQL. This qualifies application
read retry behavior in one process. Gregale's Redis-backed gateway-wide and outbound
retry budgets have separate production implementations and fleet canaries; this
case does not certify cross-process Redis coordination. Version 20 requires all
38 cases on the designated KVM runner.


## Application circuit-breaker qualification

Case 22 uses `gregale-native-circuit-breaker-test.yaml`, `native-breaker-app`, and
the existing SSLRequest provider. The fixture shares one breaker across requests
in one process: two consecutive failures open it, a monotonic three-second cooldown
permits a single half-open probe, and each dependency attempt has a 1.5-second
deadline. Open and probe-busy requests return 503 with zero attempts. Cooldown
expiry is checked lazily; it creates no background timer. Generation guards prevent
older closed-state results from resetting a circuit opened by newer failures.

The assertion driver requires two failed attempts, six fail-fast open receipts,
and an observed active half-open probe alongside four blocked callers. The probe
must fail and reopen. A second half-open probe is canceled, which must release
its slot/deadline timer and reopen conservatively. After clearing the dependency
fault, a successful probe closes the circuit and two normal reads succeed.
Independent provider evidence requires eight attempts overall, demonstrating that
the ten rejected callers never reached the dependency. Positive TLS/SSLRequest
fault counts, stable caller/provider identities and all transitions are bound in
`circuit-breaker-proof.json`. Every phase joins application requests and provider
connections before independent backend retirement; HTTP cancellation does not
claim immediate SQL cancellation.

The fixture qualifies one application's circuit-breaker behavior and does not
change Gregale's production reliability policies. Namespace-bound private controls
can stop it but cannot force state transitions. All 24 native cases and verdict
version 20 are required on the designated KVM fleet.


## Application bulkhead qualification

Case 23 uses `gregale-native-bulkhead-test.yaml`, `native-bulkhead-app`, and the
existing SSLRequest provider. A FIFO bulkhead shared within one caller process
permits two active dependency calls and two queued requests. Queue waits expire
after one second; extra requests fail immediately without reaching the dependency.
Attempts have a 2.5-second deadline, with a 3.5-second overall request deadline.
Every deadline and queue timer is explicitly cleared, including on cancellation.

The driver cancels one queued caller while two permits are held and proves removal
of its queue entry/timer. It then starts six requests, requiring two dependency
failures, two queue timeouts and two fast rejections. Three concurrent health-route
probes must respond within 750 ms while both dependency permits remain occupied.
The host verifier requires observed active/queued capacity, exact independent
provider attempt counts and matched grant/release totals. Clearing the dependency
fault permits two successful reads with fully recovered capacity.

`bulkhead-proof.json` binds all evidence to stable caller/provider identities and
one run namespace. Each phase joins application requests, permit leases, queue
entries/timers and provider connections before independent backend retirement.
Underlying database cancellation is bounded by the existing provider deadlines.
Portable regression coverage also transfers a released permit to a queued request
and shuts down active and queued work without leaking or oversubscribing permits.
This fixture qualifies one application's bulkhead behavior without changing
platform quotas or reliability policies. All 38 cases and version 20 are required
on the designated native KVM fleet.


## Combined policy qualification

Case 24 uses `gregale-native-combined-test.yaml` and an opted-in combined mode
inside `native-bulkhead-app`, reusing its FIFO permit implementation. One process
owns a two-token/30-second retry budget, a four-failure circuit breaker with a
three-second cooldown and one half-open probe, and two dependency permits plus
two queue slots. Queue waits expire in one second, attempts in 1.5 seconds, and
logical requests in 4.5 seconds. Retries use a cancelable 100 ms delay.

Policy order is explicit: check the circuit before queueing, acquire a permit,
recheck/reserve the circuit, admit any extra attempt against the shared budget,
then call the dependency. Every attempt releases its permit before retry backoff.
Half-open probes never retry or spend a retry token. Generation guards keep late
results from resetting newer circuit state. A queued request rechecks the circuit
when its permit is handed off; portable HTTP coverage verifies this race and
matched grant/release totals even when no dependency call is made.

The native driver cancels active and queued work, then verifies overload outcomes,
exactly two protected retries, responsive health probes, circuit opening and ten
rejected callers with no dependency calls. It observes one permit-backed half-open
probe, verifies failure/cancellation reopening, clears the dependency fault, and
requires a successful probe plus two normal reads while the retry budget remains
exhausted. Independent provider counters, positive SSLRequest fault evidence,
policy state, budget window and permit accounting bind `combined-proof.json` to
stable caller/provider identities. All phases join requests, permits, queues,
backoff/deadline timers and provider connections before independent backend checks.

This is application composition within one instance. Production platform retry,
breaker and capacity policies remain separate. All 38 cases and verdict version
20 are required on the designated native KVM runner.


### Repeated outage and recovery cycles

The `http-repeated` suite runs three six-caller outages against the combined
application policies. Each recovery joins all requests, permits, queue entries,
timers, connections and PostgreSQL backends. Between cycles the driver waits
for the real 30-second retry window to expire; no reset control or clock override
is available. A failed request before expiry proves retries remain exhausted.
Every new window admits exactly two retries, the circuit opens and rejects six
callers without dependency traffic, then one successful probe restores reads.
Health probes stay below 750 ms and their maximum latency varies by at most
250 ms across cycles. Stable identities, cumulative counters and independent
backend retirement bind the evidence to one application lifetime. Version 20
requires this case along with the other 25 native cases.


### Multi-dependency isolation

`gregale-native-isolation-test.yaml` deploys one caller and two independently
controlled TLS PostgreSQL providers, payment and inventory. `/a/query` and
`/b/query` each own the existing combined policies: two active permits, two queue
slots, a two-token retry window and a circuit breaker. Both execute in the same
application process and use one public HTTP listener.

The host driver stalls payment while inventory-backed reads complete, then swaps
the fault. Six successful reads must finish while the other route has two active
dependency calls; each is bounded at 750 ms. Open-circuit requests cause no
dependency traffic while three more healthy reads succeed. A successful single
probe restores the faulted route. Exact cumulative attempts, permit accounting,
policy/window state and positive provider SSLRequest evidence prevent a healthy
route from silently sharing the other route's exhausted budget or open circuit.

`isolation-proof.json` records stable identities and independent database
retirement after outage, open-circuit checks, recovery and cleanup. The gate
checks both providers' wake evidence and all three developer sessions and VM
identities. Verdict version 20 requires all 38 cases. This qualifies application
isolation within one instance; native execution requires the designated KVM fleet.

### Strict per-request retry qualification (ADR-1228)

The strict retry catalog includes four real-VM cases using the instrumented retry caller:
`req-bounded`, `req-outlier`, `req-no-id`, and `req-no-ack`. Each executes a
six-second HTTP 503 fault with at least ten selected incoming requests. The
bounded case passes; each negative case must fail its strict assertion while
the aggregate attempt average remains at most two. The outlier makes eleven
joined calls for its first correlated request and one for every subsequent
request. The other cases omit outgoing correlation or final completion receipts.
These sealed startup modes cannot be selected through a public control endpoint.

The gate requires actual gateway match evidence, strict counters, the precise
failure cause, successful namespace cleanup, and matching JSON/JUnit receipts.
Shared store conformance covers persisted counters, replacement generation
fencing, late recorder batches, and clearing the active plan. These portable
contracts do not certify the native gateway: all thirty-eight cases must execute on
the designated x86_64 KVM host against the recorded installed platform revision.


## TCP transport and resource qualification (verdict 20)

The transport catalog requires these seven cases, plus the active cancellation case described below:

| Case | Required outcome |
| --- | --- |
| `tcp-seq` | Latency, bandwidth, timeout and reset in declared order, positive gateway matches, bounded attempts, recovery and socket drain. |
| `pool-full` | Two permits and two queued callers observed, bounded queue wait, recovered completion throughput and retired waiters. |
| `pool-queue` | The same two-entry queue violates a deliberately tightened one-entry budget. |
| `req-timeout` | Complete 504 JSON timeout responses within 750ms during the stall, and complete 200 responses in baseline and recovery. |
| `req-body` | A deliberately mismatched 503 expectation fails while the application returns its 504 contract within the time budget. |
| `conn-gauge` | Reading the health endpoint as a connection gauge fails closed because `active_connections` is absent. |
| `tcp-cancel` | After the pool campaign and independent resource drain pass, the nonce checkpoint triggers CLI cancellation and namespace/VM cleanup. |

These cases use absolute paths to the sealed native-probe fixture and distinct
project/run identities. Missing samples, wrong fault order, absent resource
observations, late responses, retained sockets or waiters, and unrelated failures
cannot satisfy acceptance. JSON and JUnit receipts must match exactly, HTML must
exist, and independent API inspection must confirm removal of sessions, fault
namespaces and owned VMs. The verdict records installed platform revision,
candidate CLI digest, manifest/report digests and source identity.

Connection and pool observations are application-reported. The cancellation case
qualifies cancellation after drain; active socket, waiter and timer cancellation
is exercised by portable fixture tests, not claimed as a native in-flight drill.
The intentional queue violation retains failed high-water evidence even after
current active and queued counts reach zero; its post-drain budget verdict must
remain failed. Native acceptance requires all 38 cases on the designated KVM
host. Portable harness and real local TCP tests do not certify that host.


## Cancellation during active pool work (verdict 20)

Case 38, `tcp-flight`, supplements `tcp-cancel`, which cancels after drain. It
starts four dependency requests under an installed TCP timeout fault. A sealed
fixture mode uses two permits, two queued callers, a 10-second queue deadline and
a 30-second attempt deadline. These fixed deadlines give the native harness time
to observe and interrupt live work without adding runtime mutation endpoints.

The helper saves a nonce/run-bound baseline and active snapshot before printing
the nonce. The harness reads the current `/pool-status` through the local CLI
proxy and requires two active connections, two queued callers, two queue timers
and two attempt timers from the same application instance before SIGINT. Gregale
then kills its command helper, closes the four HTTP requests, captures at least
two production TCP fault matches and clears the fault plan.

A fresh cleanup command runs before VM teardown. It requires zero permits in use,
queued callers, sockets and timers, balanced waiter creation/retirement, at least
two cancelled callers that entered the queue and four disconnected requests, and unchanged timeout
counters. Natural timeouts, replacement processes, missing nonce/checkpoint,
retained resources or incomplete evidence fail acceptance. The final proof is
hashed in the verdict, JSON/JUnit receipts must agree, and independent API probes
still require fault namespaces, sessions and owned VMs to be gone.

Portable coverage runs real TCP chaos, the CLI reverse proxy, actual command
process termination, live checkpoint reinspection and cleanup proof validation.
It does not certify a deployed gateway or VM lifecycle. Verdict 20 requires all
38 cases on the designated native KVM fleet; the previous 37-case catalog fails.


The queued-caller cancellation counter follows a request after permit acquisition:
when an active disconnect frees a permit before a queued disconnect arrives,
that waiter may retire by acquisition and its request then cancels while active.
The gate accepts both event orders while requiring exactly two retired waiters,
two cancelled original queued callers, four cancelled requests and no timeouts.
