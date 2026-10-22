# Committed HTTP write with a lost response

Prove an ambiguous outcome: the payment fixture commits a simulated charge,
but its response body is lost. The caller retries the same key and payload at
most three times, reports `payment_outcome: unknown`, and reconciles that key
after the fault clears without charging again. No real payments are processed.

From this directory, with an updated CLI, API and gateway deployed:

```sh
gregale test --suite http-write --validate
gregale test --suite http-write --preflight
gregale test --suite http-write --max-workload-minutes 20 \
  --report writes.json --html writes.html --junit writes.xml
```

The caller sends HTTP POST through `payment.svc.gregale:10080`. The payment
fixture binds an idempotency key to a canonical `{customer, sku, quantity}`
payload and persists the charge before returning HTTP 201. A reused key with a
different payload returns 409 without changing the charge. The run-scoped
`http_response_lost` fault forwards each request and drains the successful
response, then declares a one-byte body but sends none. Real clients detect an
incomplete entity; the caller cannot treat a successful status alone as a
confirmed result. Non-success responses, including conflicts, pass through.

The staged assertions establish one baseline charge, exercise three failed
acknowledgements for a second key, inspect the provider's committed ledger and
exact attempt count independently, reject a changed payload during the fault,
then explicitly clear and replay the committed operation. Successful replays
and changed-payload conflicts after recovery must leave exactly two total
charges. Reports include positive fault-match evidence and the final same-key
confirmation; command logs contain attempts and response-read observations.

The provider's version-3 ledger uses atomic replacement, file and directory
fsync, and immutable payload fingerprints. Portable tests reopen the provider
from the same file, exercise concurrent same-key requests, and prove a deadline
can leave one committed charge with an unknown acknowledgement. This is a
single-writer example; use transactional shared storage and a provider's real
idempotency contract in production. VM replacement does not preserve this
fixture's local disk. Caller retries are safe only because the provider binds
the exact key/payload before committing; this is not a generic POST retry policy.

`http_response_lost` uses the existing authorized route and forwarding policy;
it adds no forwarding attempt. It discards successful response bytes without
buffering an entire body. Non-2xx, 204, 205, empty responses, HEAD and upgrades
are not fault matches. Cancellation is not counted as an injection. Selection
is latched for the request: already-selected requests can complete after clear,
while clear and lease expiry stop new selections. The transport's request
budget governs draining; the caller uses a five-second deadline. The fault
alone does not prove a database commit—the independent ledger checks do.

Portable checks from this directory:

```sh
node --test test/write.test.mjs
```

Those checks and the gateway's real HTTP/1 and HTTP/2 protocol tests do not
qualify a deployed fleet. Native execution requires the new API/gateway contract.

## Replace the caller during a write

`gregale-in-flight-test.yaml` holds a new `in-flight` charge after persisting its
payload-bound intent. Read-only caller and provider checkpoints establish that
the write is active and uncommitted. Gregale cold-replaces the caller, verifies
an idle replacement and the surviving provider's ledger identity, baseline
charge and pending intent, then replays the same key. Matching replay commits
once and releases the held operation; conflicting payloads return 409 without
releasing it. Four replays must leave exactly two charges including baseline.

```sh
gregale test --manifest gregale-in-flight-test.yaml --suite http-in-flight --validate
gregale test --manifest gregale-in-flight-test.yaml --suite http-in-flight --preflight
gregale test --manifest gregale-in-flight-test.yaml --suite http-in-flight \
  --max-workload-minutes 20 --report in-flight.json --html in-flight.html --junit in-flight.xml
node --test test/*.test.mjs
```

This fixture holds the first matching key for at most 90 seconds; only replay
releases it. A timeout leaves an uncommitted pending intent and fails closed for
that provider process. Reopening the provider ledger permits reconciliation
without retaining a stale gate. An original caller disconnect does not cancel
the accepted provider intent. Caller deadlines are also 90 seconds for this
scenario; the restart step is bounded to one minute. Fixture teardown explicitly
cancels held work. Ordinary response-loss scenarios keep their five-second
caller deadline and do not hold writes.

Portable tests kill an actual caller subprocess with SIGKILL at this checkpoint,
start a fresh caller, and prove single-charge replay and conflict rejection.
Native tests use the managed fresh replacement API; draining policies may cause
this scenario to time out, which is a failure. Reports distinguish a request
that ended from one Gregale canceled after verified replacement. These are
different evidence from a SIGKILL. Native qualification requires running the
extended acceptance gate on a KVM host; portable checks alone do not qualify it.

## Restart at each write checkpoint

`gregale-restart-matrix-test.yaml` runs three isolated scenarios in order using
Gregale's suite runner. Each uses the same canonical order and idempotency key
in its own workspace and surviving provider, then cold-replaces only the caller:

| Scenario | Provider checkpoint | Charges including baseline | Pending intents |
| --- | --- | --- | --- |
| `write-before-acceptance` | Request received, held before durable acceptance | 1 | 0 |
| `write-pending` | Payload-bound intent persisted, held before commit | 1 | 1 |
| `write-committed` | Charge persisted, held before acknowledgement | 2 | 0 |

Here acceptance means a persisted business intent, not acceptance of a TCP
connection. The before-acceptance gate binds the payload in provider memory;
it has no durable intent for that key. The other checkpoints bind it in the
ledger. An actively held gate, caller-active assertion, preserved ledger identity,
expected counts, and completed cold replacement are all required before replay.
Matching replay commits or reads the same operation and releases its gate;
conflicting replay returns 409 and cannot release it. Four replays plus conflicts
must leave exactly two charges including baseline, no pending intents, no held
gates, and six additional provider attempts. No real payments are processed.

```sh
gregale test --manifest gregale-restart-matrix-test.yaml --suite http-restart-matrix --validate
gregale test --manifest gregale-restart-matrix-test.yaml --suite http-restart-matrix --preflight
gregale test --manifest gregale-restart-matrix-test.yaml --suite http-restart-matrix \
  --max-workload-minutes 60 --report matrix.json --html matrix.html --junit matrix.xml
node --test test/*.test.mjs
```

JSON, HTML, JUnit, and terminal results identify the interrupted checkpoint by
scenario name. The suite continues after a failed case by default, completing
cleanup before the next case. Any failure or skipped case produces a nonzero
exit. Omit `--fail-fast` for complete coverage; interruption records the remaining
cases as skipped. `--repeat` repeats each isolated scenario using fresh run IDs.
The preflight checks capacity and the resource guard covers the complete suite.

Configure the example provider with `HOLD_FIRST_KEY` and `HOLD_STAGE` set to
`before-acceptance`, `pending` (the default), or `committed`. These fixture gates
are bounded to 90 seconds. Expiration or teardown leaves an uncommitted intent
uncommitted and fails closed for that process. If the operation already committed,
replay can recover it after acknowledgement-hold expiration. Reopening a pending
or committed ledger reconciles its record without recreating an in-memory gate;
a before-acceptance operation has no persisted record to reconcile. Caller
replacement does not replace this external provider or preserve caller VM disk.

Portable tests kill a real caller subprocess with SIGKILL at every checkpoint,
inspect the on-disk ledger at that boundary, and prove replay, concurrent retries,
conflicts, timeouts, and teardown. They also reopen the provider while a committed
acknowledgement was held. Native acceptance verdict version 4 requires all eleven
cases, including all three checkpoint scenarios and independent API cleanup.
Managed replacement is still distinct from SIGKILL, and a draining policy that
cannot replace the held caller within the deadline fails this campaign.

## Replace the payment dependency with a Postgres ledger

`gregale-dependency-restart-test.yaml` declares `http-dependency-restart` with
`dependency-pending` and `dependency-committed`. The caller stays alive while
Gregale cold-replaces payment. The provider stores the payload-bound operation
in external Postgres, so replacement discards its process and VM disk without
discarding the ledger. Readiness requires a healthy database and no inherited
in-memory hold. Before/after storage probes require the same ledger identity
and the pending or committed record. Matching replay reconciles once; changed
payloads remain 409. Concurrent replay must leave exactly two charges including
the baseline, no pending intent, and no hold.

Supply an operator-owned disposable Postgres database reachable by both the
runner and the payment VM. Schema creation runs separately with a migration
credential; serving receives only a runtime credential with USAGE on public
and SELECT/INSERT/UPDATE/DELETE on `gregale_write_ledgers` and
`gregale_write_charges`. The runtime must not own the schema or have CREATE.
The example uses an explicit external connection because automatic scenario
`postgres: true` binds a restricted runtime credential, not a migration
credential. The serving process refuses `MIGRATION_DATABASE_URL` and requires
explicit disposable-database opt-in. No privileged credential is injected into
payment and serving never creates tables.

Install the pinned local driver for fixture commands, then migrate once:

```sh
npm --prefix payment-postgres ci --ignore-scripts --no-audit --no-fund
# MIGRATION_DATABASE_URL is supplied securely to this separate process.
node payment-postgres/migrate.mjs
```

Configure `GREGALE_DEPENDENCY_DATABASE_URL` securely with the restricted runtime
URL and set `GREGALE_DEPENDENCY_DATABASE_DISPOSABLE=1`. For remote connections,
use TLS with certificate verification (`sslmode=require` or `verify-full` and
ordinary Node CA trust). `sslmode=disable` is for an explicitly configured local
fixture. Do not commit connection strings. The manifest's `${env.NAME}` secret
references are resolved only at execution; validation needs no credential,
and preflight checks presence without printing values.

```sh
gregale test --manifest gregale-dependency-restart-test.yaml --suite http-dependency-restart --validate
gregale test --manifest gregale-dependency-restart-test.yaml --suite http-dependency-restart --preflight
gregale test --manifest gregale-dependency-restart-test.yaml --suite http-dependency-restart \
  --max-workload-minutes 40 --report dependency.json --html dependency.html --junit dependency.xml
```

Each run uses its own 32-character run ID as a namespace. SQL primary keys bind
`(namespace, operation_key)` and transactions lock the existing row, reject
fingerprint conflicts, and only advance pending to confirmed. There is no
mutation retry inside a failed transaction; an uncertain acknowledgement is
reconciled by explicit same-key replay. Readiness/database errors return redacted
503 rather than switching to an empty local ledger. SQL has connection, query,
statement, lock, and idle-transaction deadlines. The 90-second HTTP hold occurs
outside the database transaction, without reserving a pool connection.

Cleanup deletes only the run's namespace and cascades to its charge rows; it
never truncates tables or drops schemas. It checks both tables for retirement.
Native qualification also independently checks exact namespace retirement and
requires the service's planned restart wake ID without accepting unexpected
wakes. Verdict version 17 requires all twenty-six cases, including both dependency
checkpoints; an older eleven-case verdict does not qualify it.

From the repository root, run the real Postgres portable gate:

```sh
make test-scenario-dependency-postgres
```

This gate owns one disposable local Postgres 16 Docker container, migrates its
schema, grants a separate runtime role, and always removes the container. It
uses real provider subprocess SIGKILL/restart, four concurrent HTTP replays,
multiple concurrent SQL writers, namespace isolation, schema/privilege failures,
failed COMMIT rollback, credential redaction, and cleanup. It never treats skips
as a pass. These results prove the local Postgres recovery contract; managed VM
replacement still needs the designated KVM fleet and a migrated external fixture.

## Ambiguous Postgres COMMIT acknowledgement

The real-Postgres portable gate also tests a successful database COMMIT whose
acknowledgement never reaches payment. A bounded local TCP fixture forwards the
original transaction, then observes Postgres `CommandComplete(COMMIT)` and idle
`ReadyForQuery`. It suppresses both messages and closes the connection. An
independent administrator connection must find the confirmed row before replay;
positive dropped-acknowledgement evidence is required. A transport failure by
itself cannot establish that the operation committed.

The ledger classifies a lost/uncertain COMMIT response as
`commit_outcome_unknown`, destroys that connection, and does not retry the SQL
mutation or infer rollback. Payment returns HTTP 503 with
`payment_outcome: unknown` and `retryable: true`; the caller also reports unknown
without automatically sending another write. Explicit same-key replay confirms
the persisted result, four concurrent replays leave one charge, and changed
payloads remain 409. Recovery assertions require zero pending intents, active or
queued writes, pool waiters and idle transactions; proxy shutdown joins both
socket ends. Neither response bodies nor SQL/authentication frames are logged.

A nonfatal SQL statement rejection is separately tested through the same armed proxy: its error
passes through, no acknowledgement loss is counted, and the pending intent
remains pending until an explicit successful replay. Fragmented protocol frames,
unarmed forwarding, rejection handling and socket joining have portable tests.

```sh
# From the repository root; owns and removes one local Postgres 16 container.
make test-scenario-dependency-postgres
```

The protocol fixture supports explicit plaintext or verified TLS on loopback
connections inside this disposable test. The COMMIT-loss case supplies portable
protocol/recovery evidence; the native twenty-six-case verdict still covers the
deployed restart matrices and requires a KVM runner. Deployed fault-proxy routing
and native TLS COMMIT-loss qualification remain separate work.

### Verified TLS COMMIT-loss testing

The same gate runs provider and caller acknowledgement-loss cases with TLS on
both proxy legs. `createCommitProxy(databaseURL, { tls: { key, cert, ca,
servername } })` accepts `sslmode=verify-full` (or `require`, which also verifies)
and requires an explicit proxy identity, upstream CA and upstream server name.
The client independently trusts and verifies the proxy certificate using the
normal Node trust store. The proxy negotiates PostgreSQL SSLRequest, terminates
client TLS and connects to PostgreSQL with certificate and hostname verification
and a minimum of TLS 1.2. Each handshake has a two-second absolute deadline, excluding explicitly configured
SSLRequest stalls described below.
It never falls back to plaintext. Opt-in termination is
needed to inspect COMMIT frames; this is not a transparent encrypted TCP relay.

The Docker gate requires OpenSSL and creates a fresh one-day loopback identity in
a private temporary directory. Its private key is copied only into the owned
container's temporary filesystem and removed with the container; host files are
also removed on failure. The Node subprocess trusts the disposable certificate
through `NODE_EXTRA_CA_CERTS`, retaining inherited additional roots. Serving code
keeps its existing verified-TLS defaults. No reusable key or certificate is
checked into the repository.

Evidence includes verified upstream connection and TLS failure counts alongside
COMMIT/drop counters. Independent `pg_stat_ssl` queries require encrypted TLS
1.2/1.3 recovery sessions. Certificate trust and hostname mismatches on either
leg, plaintext client attempts and upstream TLS refusal must fail before
forwarding SQL or consuming the armed fault. Shutdown joins incomplete client
and upstream negotiations as well as established sockets. Fragmented encrypted
protocol replies preserve rejection/rollback handling. These checks do not
qualify client-certificate authentication, remote targets or native VM routing.

### Controlled COMMIT latency and timeout tests

The loopback PostgreSQL fixture can hold one explicitly selected COMMIT request
or successful acknowledgement over plaintext or verified TLS:

```js
// Let the successful acknowledgement arrive after the caller's deadline.
proxy.arm({ mode: "delay", direction: "response", durationMS: 1200 });
// Hold COMMIT before forwarding it, then close the connection at the bound.
proxy.arm({ mode: "stall", direction: "request", durationMS: 1200 });
// Existing immediate acknowledgement loss remains available.
proxy.arm();
```

`delay` releases the held frames in order when the duration expires. `stall`
closes both connection ends at that bound, forwarding no held frames. Durations
must be integer milliseconds from 1 through 5000. Direction defaults to
`response`. Request faults fire before forwarding the next simple-query COMMIT;
response faults require successful `CommandComplete(COMMIT)` and idle
`ReadyForQuery` first. Rejected COMMITs and rollback-tag results pass through
response faults without creating a successful-fault record. This targets COMMIT
frames, not every query, authentication, TLS negotiation or network packet.

`evidence()` includes `faults_fired`, `active_faults`, `fault_timers`, and bounded
`faults` records containing mode, direction, configured/measured milliseconds and
`holding`, `released`, `timed_out`, `canceled` or `dropped` outcome. Timings use a
monotonic clock. A proxy accepts at most 100 arm operations, so evidence cannot
grow indefinitely. Client disconnects and shutdown cancel held frames and timers;
shutdown joins their relay work as well as socket and handshake cleanup. Frame
buffers remain bounded, and backpressure/order are preserved across fragmented
and coalesced messages. No operation keys, payloads or credentials enter fault
evidence.

The real-Postgres gate verifies TLS request/response delays and bounded stalls
with a 400 ms caller deadline and 1200 ms hold, plus an acknowledgement delay
beyond the database client's four-second query deadline. An independent SQL
connection witnesses confirmed data while response acknowledgements are held,
and pending intent while request COMMITs are held. A caller timeout reports an
unknown outcome even when the provider subsequently receives a late successful
acknowledgement. A database-client timeout discards its uncertain connection and
cancels the proxy timer. A stalled request preserves the previously committed
pending intent until explicit replay; the test does not infer rollback from its
HTTP failure alone.

Each case requires one original provider attempt, changed-payload rejection,
four explicit matching replays leaving one charge, encrypted recovery sessions,
zero active/queued work, pool waiters, idle transactions, fault timers and joined
connections. The native twenty-six-case gate separately requires the deployed authentication case; deployed
routing and native latency qualification remain pending.

### Bandwidth-limited PostgreSQL reads

The opt-in loopback proxy can pace PostgreSQL application bytes independently in
each direction, including encrypted connections:

```js
// Enable after connecting/warming the client to isolate query transfer traffic.
proxy.limitBandwidth({ direction: "response", bytesPerSecond: 8192 });
// Apply separately when both directions should be constrained.
proxy.limitBandwidth({ direction: "request", bytesPerSecond: 32768 });
// Release queued bytes in order and restore normal transfer speed.
proxy.clearBandwidth("response");
```

Rates are integer bytes per second from 1024 through 16777216. Each direction
shares one aggregate budget across all proxy connections. Frames are forwarded
in small paced chunks with no initial burst; slower receiver backpressure can
further reduce the achieved rate. TLS negotiation itself is excluded. PostgreSQL
startup/authentication frames are included if the limit is enabled before client
connection, so enable it after a verified warm-up to isolate a large read. This
measures plaintext application-frame bytes accepted by the destination socket,
not encrypted network bytes or proof of peer delivery.

`evidence().bandwidth` records direction, configured rate, forwarded bytes,
measured monotonic elapsed time, canceled transfers and `active`, `cleared` or
`closed` outcome. Live evidence also reports active limits, pending transfers,
buffered bytes and timers. At most 100 limit records, 16 accepted connections,
1 MiB protocol frames and 8 MiB of pending limiter bytes bound the fixture.
Removing a limit wakes paced transfers without discarding remaining frame bytes.
Client closure cancels that transfer; proxy shutdown cancels all limits and joins
paced writes, relays, handshakes and sockets. Evidence contains no SQL, credentials
or row contents. Frame buffers and queued transfer counts return to zero at
quiescence/shutdown; unused active controls must be explicitly cleared or closed.

The real PostgreSQL gate exercises verified-TLS large reads that complete under
a limit, complete after removing a limit midway, exceed a database-client read
deadline, and are interrupted by proxy shutdown. It requires positive transfer
and rate evidence, complete payload equality for successful reads, fresh
unlimited recovery reads, encrypted idle recovery sessions and zero retired
backend sessions/timers/buffers/connections. A timed-out partial read explicitly
retires its PostgreSQL connection before recovery; it must not reuse a stream
containing an incomplete response. Portable checks cover both directions,
aggregate concurrency, byte ordering and cancellation. These remain fixture
checks; deployed routing, COPY/replication and native bandwidth qualification
require separate support/evidence.

### Interrupting a TLS read inside a row

The proxy can cut one connection after forwarding a configured prefix of the
next sufficiently large PostgreSQL `DataRow` response frame:

```js
// Arm after a verified connection and warm-up query.
proxy.armReadCut({ afterBytes: 32768 });
// Remove an unused cut, or allow an in-progress prefix to finish its full row.
proxy.clearReadCut();
```

`afterBytes` is an integer from 1 through 1048575, counted from the start of the
selected DataRow frame, including its type/length header. A one-column text row
also includes column-count/field-length bytes before its value. Rows whose whole
frame is no larger than the threshold pass through and leave the cut armed.
Startup/authentication and other response frames are unaffected. Once a row is
selected, the cut belongs to that connection; concurrent streams continue and
new connections are unaffected by this one-shot fault. The parser first receives
the bounded full upstream row (at most 1 MiB), then forwards only its prefix to
the client. This targets a partial client response, not upstream query execution.

The prefix passes through any configured response bandwidth limit. After a local
write-flush callback confirms queued prefix bytes, the proxy destroys both
connection ends and discards the remaining response. This does not promise a TCP
RST packet, TLS close-notify, or remote peer delivery. Portable receiving clients
must independently observe the exact prefix. Clearing an in-progress cut forwards
the remainder in order without interrupting the connection; peer closure or
proxy shutdown cancels it without counting a fired cut.

Evidence reports `read_cut_armed`, `active_read_cuts`, `read_cuts_fired` and up to
100 `read_cuts` records. Records contain configured offset, selected frame size,
locally flushed prefix bytes, elapsed monotonic time and `armed`, `forwarding`,
`cut`, `cleared` or `canceled` outcome. Positive cut evidence requires completion
of the prefix flush; cancellation is not proof that a cut fired. Row values, SQL,
credentials and connection identities are not retained in these records.

The real PostgreSQL gate cuts a verified-TLS 64 KiB read near the midpoint of its
row, with and without response pacing. The client must reject the query and emit
zero row events. It retires the broken connection, independent SQL verifies the
old backend is gone, and a fresh backend returns the complete payload over
verified TLS. Unreached and pre-cleared cuts provide negative controls. Final
checks require encrypted idle recovery, no second cut and zero remaining backend
sessions, active cuts, transfer buffers, timers or connections. Native/deployed
qualification remains a separate release gate.

### Connection establishment stalls at SSLRequest

An opt-in TLS proxy can accept TCP, validate PostgreSQL's SSLRequest, and withhold
its `S` response before opening any upstream connection:

```js
proxy.stallConnections({ phase: "ssl_request", durationMS: 1500 });
// Release still-open attempts into normal verified TLS; disable new stalls.
proxy.clearConnectionStall();
```

The rule persists across new connection attempts until cleared. Each accepted
SSLRequest gets its own bounded hold, with an integer duration from 1 through
5000 ms. Expiry closes the connection without sending a TLS/plaintext acceptance;
client disconnect and proxy shutdown cancel the hold. Clearing releases existing
holds and starts ordinary certificate/hostname-verified TLS on both legs. This
fault targets the client SSLRequest response, not authentication, certificate
validation or an upstream handshake.

The normal two-second TLS negotiation bound is suspended only for an explicitly
configured hold; a fresh two-second TLS handshake bound applies after release.
Malformed/incomplete SSLRequest traffic still fails under the original parser
and negotiation deadline. The application's connection deadline remains
independent and can expire first. There is no plaintext fallback, credential
forwarding or automatic retry loop in the proxy. Use an explicit application
retry budget for connection acquisition; these tests do not repeat writes.

Evidence includes TCP `connection_attempts`, `ssl_requests_seen`, live
`pending_connections`, `connection_stall_enabled`, active stalls/timers and up to
100 `connection_stalls` records. Each fired record contains phase/leg, configured
and elapsed monotonic milliseconds, and `holding`, `released`, `timed_out` or
`canceled` outcome. At most 100 control activations and 100 fired holds are
accepted; additional held attempts fail closed. No endpoint identities,
authentication frames, credentials or SQL enter the records.

The real PostgreSQL gate checks three explicit failed acquisition attempts,
queued acquisitions under a one-connection pool, release into verified TLS,
stall expiry and shutdown during negotiation. Failed acquisitions must not
produce a usable client or an authenticated backend session. Recovery requires
one encrypted idle pool connection, no remaining waiters and no hidden retries;
final cleanup requires zero sockets, pending negotiations, stall timers, pool
clients and backend sessions. Portable tests also reject malformed SSLRequests
and untrusted client-side server certificates after release. Authentication-phase stalls are described below; native/deployed qualification
remains separate work.

### Authentication-phase stalls after verified TLS

Use the same connection-stall control to withhold the first PostgreSQL
password/SASL authentication challenge after both TLS connections are verified:

```js
proxy.stallConnections({ phase: "authentication", durationMS: 1500 });
// Resume the original login exchange and disable stalls for new connections.
proxy.clearConnectionStall();
```

This phase accepts initial cleartext-password, MD5-password and SASL challenge
frames; real PostgreSQL acceptance uses its normal SCRAM exchange. Challenge
framing is checked before counting positive fault evidence. TLS verification,
server-selected authentication mechanisms and the original challenge bytes are
preserved. Later SASL continuation/final messages are unaffected. Authenticated
sessions and trust-login AuthenticationOK responses do not create a stall;
authentication rejections pass through normally. This does not add certificate
or password bypasses and never injects, stores or logs login secrets.

The persistent rule, 1–5000 ms hold bounds and 100 control/record caps are shared
with SSLRequest stalls. Expiry/disconnect/shutdown cancels the login connection;
clear forwards the held challenge and resumes normal authentication. Evidence
identifies the `authentication` phase, counts validated
`authentication_challenges_seen`, and reports `pending_authentication` separately
from TLS `pending_connections`. Pending authentication begins once both TLS legs
are verified and ends at initial ReadyForQuery or connection closure; TLS
verification alone does not imply a usable database client.

The real PostgreSQL gate exercises three caller-selected attempts, queued
acquisitions, release, expiry and shutdown for both establishment phases. Failed
authentication-phase acquisitions produce no usable client. Recovery requires an
encrypted idle client and zero pool waiters. Independent SQL checks that every
new client-backend PID created during the case retires, including attempts that
never completed login. A separate wrong-password test verifies that releasing a
stall still returns PostgreSQL's authentication failure before healthy recovery.
Portable tests cover challenge withholding/release, cancellation, AuthenticationOK,
rejection and malformed challenge negative controls. Final cleanup requires zero
pending logins, stall timers, sockets, pool clients and backend sessions.
Additional authentication-message faults and native/deployed qualification
remain separate work.

### Deployed authentication-stall scenario

`gregale-native-auth-test.yaml` adds a `real-vm`, warm-profile readiness scenario
to native acceptance. The application queries a payment fixture that embeds the
same proxy on guest loopback and connects to external PostgreSQL with verified
TLS. A baseline SELECT returns 42. The application assertion command proves three
bounded authentication failures, clears the fault, and verifies two successful
reads on fresh database connections. Cleanup stops the proxy/pool and independently
checks backend retirement, followed by normal VM/developer workspace cleanup.

Run on the designated native host with the prerequisites in
[`scenario-restart-native-acceptance.md`](../../docs/scenario-restart-native-acceptance.md):

```sh
make native-scenario-restart-acceptance
```

The version-17 gate requires twenty-six cases and a separate run-bound
`auth-proof.json` with positive fault, deadline, TLS, recovery and cleanup evidence.
The runner generates ephemeral TLS identity/control secrets and sends them through
sealed workload bindings. The control endpoint requires the token and namespace;
no key material or credentials enter reports. This read-only fixture mode is
explicitly selected and does not change the normal write provider. Portable
Postgres acceptance exercises the same lifecycle; only a complete native verdict
qualifies deployed behavior. This workspace's native invocation is blocked by
missing `/dev/kvm`, so it provides no deployed qualification claim.


### Deployed lost COMMIT acknowledgement

`gregale-native-commit-test.yaml` deploys the existing write caller and PostgreSQL
ledger with a loopback TLS fault proxy inside payment. The assertion command arms
one successful-COMMIT reply drop, requires an unknown outcome without automatic
retry, and independently checks that the row is already confirmed. Explicit
same-key replay returns the committed result; a different payload returns 409;
the database still contains exactly one matching charge and no pending rows.
Cleanup stops the fixture and removes only the run's ledger namespace.

The native acceptance gate now requires all twenty-six cases and a version-17 verdict,
including `commit-proof.json`, verified TLS, exact attempts/commit counts, stable
provider/ledger identities, joined connections, and independent database/backend
cleanup. Run `make native-scenario-restart-acceptance` on the designated KVM host
with the external migrated disposable PostgreSQL fixture. This workspace's missing
`/dev/kvm` still prevents native qualification; local PostgreSQL checks certify only
the fixture and acceptance contracts.


### Deployed COMMIT deadlines

The request and response manifests `gregale-native-commit-request-test.yaml` and
`gregale-native-commit-response-test.yaml` hold COMMIT for five seconds while the
caller deadline is 1.5 seconds. Both return unknown outcome. After database work
retires, an independent observer distinguishes a request never committed (zero
charges) from a successful commit with a withheld reply (one charge). Same-key
replay ends with exactly one confirmed charge; changed-payload reuse returns 409.

The native gate requires canceled holds, deadline evidence, exact attempts/commit
counts, stable identities, joined connections/timers, and independent cleanup.
The caller abort does not immediately cancel SQL; the existing database deadlines
bound and retire that work before replay. Version 17 requires all twenty-six cases.
Native qualification still needs the designated KVM runner.


### Deployed bandwidth restriction

`gregale-native-bandwidth-test.yaml` deploys a read-only 64 KiB PostgreSQL probe
through the same loopback verified TLS proxy. A 256 KiB/s response restriction
permits a complete read, checked by byte count and SHA-256. At 16 KiB/s the
1.5-second query deadline returns 503; partial data cannot count as success.
Removing the limit restores the complete read. Every acquisition is retired.

The version-17 native gate requires all twenty-six cases, including positive pacing,
canceled-transfer evidence, exact attempts/TLS counts, stable instance identity,
empty buffers/connections/timers, and independent backend retirement. Rates count
PostgreSQL protocol plaintext writes rather than encrypted wire bytes or confirmed
peer delivery. No ledger rows, schema changes or migration credentials are needed.
Native qualification still requires the designated KVM fleet.


### Deployed read interruption

`gregale-native-read-cut-test.yaml` arms a one-shot cut after a 2048-byte prefix
of the fixed 64 KiB PostgreSQL result row. A partial read must return 503; a fresh
connection returns the complete byte count and SHA-256. A second cut runs with two
independent acquired clients, synchronized by a two-second rendezvous: exactly
one read fails and the other succeeds. A final fresh read succeeds without another
cut. A missing participant fails closed and releases its client/timer.

The version-17 gate requires all twenty-six cases, including two positive cut records,
a completed concurrent pair, exact attempts/TLS counts, stable identity, no remaining
connections, buffers or timers, and independent backend retirement. Prefix evidence
is a local flush receipt; exact peer prefix delivery is covered by the portable
protocol tests. Native qualification still requires the designated KVM fleet.


### Deployed connection-establishment stall

`gregale-native-ssl-request-test.yaml` withholds PostgreSQL's initial SSL response
for five seconds, before TLS or upstream login. The existing two-second connection
timeout bounds three explicit retries. Three concurrent requests then share one
pool slot: queue depth two must be observed, every request must finish with 503,
and actual connection attempts remain bounded. Clearing the fault restores two
fresh verified TLS queries returning 42.

The version-17 gate requires all twenty-six cases, with canceled pre-TLS hold evidence,
exact acquisition/SSLRequest counts, no extra TLS/login during the fault, stable
instance identity, empty pool/negotiation/socket/timer resources, and independent
backend retirement. The native qualification still requires the designated KVM
fleet; local PostgreSQL tests validate the exact fixture and proof contracts.


### Shared retry budget under concurrent failures

`gregale-native-retry-budget-test.yaml` deploys six concurrent read callers sharing
two extra attempts per 30-second window in one application process. Each request
has a 1.5-second attempt deadline, a cancelable 100 ms retry delay, and a 4.5-second
overall deadline. Under an SSLRequest stall, exactly two of six callers retry,
producing eight dependency attempts overall. Cancellation before admission spends
no token; cancellation during a reserved delay retains the spent token and sends
no extra request. Originals and first-attempt recovery remain available when the
budget is exhausted.

The version-17 gate requires all 26 cases, exact caller/provider attempt counts,
a stable budget window and instance identities, bounded queues, joined requests
and cleared deadline/backoff timers, plus independent database cleanup. This
fixture qualifies an application budget shared within one process; existing
Redis-backed gateway fleet budgets have their own canaries. Native qualification
still requires the designated KVM runner.


### Application circuit breaker

`gregale-native-circuit-breaker-test.yaml` deploys a breaker shared within one
application instance. Two dependency failures open the circuit for a three-second
cooldown. Open requests fail fast without dependency calls; half-open mode admits
one probe and rejects concurrent callers. Failed or canceled probes reopen the
circuit and release their slot/timer. Clearing the dependency fault permits a
successful probe to close it and restore normal reads.

The version-17 gate requires all 26 cases, exact state/generation transitions,
positive single-probe observations, unchanged provider counts for ten rejected
callers, verified TLS recovery, joined requests/deadline timers, and independent
backend retirement. Late successful requests cannot reset newer open state.
This fixture qualifies application behavior in one process; native qualification
still requires the designated KVM fleet.


### Application bulkhead

`gregale-native-bulkhead-test.yaml` limits one application's dependency concurrency
to two calls and a FIFO queue of two requests. Queue deadlines are one second;
extra requests fail immediately. Under the SSLRequest stall, six requests must
produce two dependency failures, two queue timeouts and two rejections. A separate
healthy route responds while dependency capacity is occupied. Cancellation removes
a queued caller, and recovery restores successful reads with all permits released.

The version-17 gate requires all 26 cases, positive active/queued observations,
matched grant/release counts, exact provider attempts, responsive health probes,
cleared queue/deadline timers and independent backend retirement. A portable
regression also checks queued permit handoff and shutdown cleanup. Qualification
remains scoped to one application instance and requires the designated KVM runner.


### Combined application resilience policies

`gregale-native-combined-test.yaml` composes the shared retry budget, circuit
breaker and existing FIFO bulkhead in one application instance. Each retry
reacquires a permit and rechecks the circuit. Half-open probes have one protected
attempt and cannot consume retries. Active/queued cancellation, overload, health
isolation, circuit reopening and successful recovery are verified together.

The version-17 gate requires all 26 cases, two shared retry admissions, permit
protection for every dependency call, no calls for open/probe-busy requests,
a single half-open probe, unchanged exhausted budget on recovery, and joined
resources plus independent backend retirement. A portable regression verifies
that a queued request cannot bypass a circuit opened before permit handoff.
Native qualification still requires the designated KVM fleet.


### Multi-dependency isolation

Run `gregale test --manifest examples/scenario-http-write/gregale-native-isolation-test.yaml --suite http-isolation --validate`
to validate the two-provider manifest. Native execution uses `--engine real-vm`
and the sealed disposable PostgreSQL/TLS fixture environment described in
[the native acceptance guide](../../docs/scenario-restart-native-acceptance.md).

One application exposes payment-backed `/a/query` and inventory-backed `/b/query`.
Each has separate concurrency, queue, retry-budget and circuit-breaker state.
The driver stalls each provider in turn and requires six healthy reads to
complete during the other route's saturation, below 750 ms per read. It checks
fail-fast circuit rejection, further healthy traffic, successful recovery,
matched permit accounting and zero live resources and database backends.

The version-17 gate requires all 26 cases, both hot-provider receipts and cleanup
of all three workloads. Portable tests cover cancellation and demonstrate that
saturating either dependency leaves the other route's retry budget usable.

### Reusable assertions for your application

`gregale-assertions-test.yaml` expresses healthy inventory latency and payment
recovery with named assertion profiles. The inventory profile is reused during
baseline, outage and recovery; the payment profile adds a four-second sustained
recovery deadline after the dependency fault clears. Reports show profile names,
per-route measurements and failing budgets. Copy these profiles to a manifest
for your application and change the named HTTP requests and limits.

Validate with:

```sh
gregale test --manifest examples/scenario-http-write/gregale-assertions-test.yaml --suite http-assertions --validate
```

See [reusable assertion profiles](../../docs/scenario-tests.md#reusable-route-assertion-profiles)
for validation and report details. Executing this reference fixture needs the
sealed disposable PostgreSQL/TLS environment used by native isolation tests.

The HTTP assertion example also references `payment-retry-limit` during the
outage. Its `dependency_attempts` budget requires at least three incoming
`payment-read` samples and limits the phase-wide average to two HTTP dependency
calls per sample. Reports include both counts, the ratio and the limit. Missing
samples or incomplete proxy evidence fail the budget. The profile uses the
phase's single full-coverage HTTP status rule to identify dependency traffic.

### Strict per-request retry limits

`gregale-per-request-attempts-test.yaml` adds `per_request: true` to a reusable
HTTP dependency-attempt profile. It limits every attributed request to two
attempts and uses the retry fixture's scoped ID propagation and final count
receipt. Reports identify outliers even when the phase average stays within its
limit. Missing evidence and the 1,024-request phase capacity fail explicitly.

```sh
gregale test --manifest examples/scenario-http-write/gregale-per-request-attempts-test.yaml --suite per-request-attempts --validate
```

See [per-request retry limits](../../docs/scenario-tests.md#per-request-retry-limits)
for the application instrumentation contract. This fixture needs the same sealed
disposable PostgreSQL/TLS environment as other native retry fixtures, plus an
updated gateway providing request correlation counters.
