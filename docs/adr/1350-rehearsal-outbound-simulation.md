# ADR-1350 · Rehearsal outbound simulation

- **Status:** accepted; simulation enrollment is disabled pending native qualification
- **Date:** 2026-10-17
- **Decision:** Add immutable, run-bound HTTP and Resend fixtures to contained serving-app enrollment. Authenticate simulation relay connections with a separate host-only, run-specific audience. Persist each request attempt and completion, then include them in a version-2 sealed action inventory.
- **Why:** Containment blocks direct external effects but cannot return the responses an application needs to exercise its import and retry paths. Managed webhook intent alone does not describe requests made through integration clients.
- **Consequences:** Simulation never resolves a live integration, loads provider credentials or calls an HTTP transport. Recorded unknown fixtures, invalid requests, interrupted calls and overflow make evidence incomplete. Unavailable storage refuses admission. Existing webhook-only version-1 runs keep their scope.
- **Rejected alternatives:** Reopening the live relay for contained apps, routing a failed fixture to a provider, treating retries as one set element, or sealing while a delayed simulation is still active.

## Authority and compatibility

Registration accepts `simulation: {version: 1, fixtures: [...]}` only with
`network_mode: contained`, `webhook_mode: capture` and
`action_evidence_version: 2`. Every member receives the same normalized plan,
validated against the complete logical workload inventory. The retained JSONB
plan is immutable and its canonical SHA-256 is echoed after the transaction
commits. The Go client checks that acknowledgement before configuration or
deployment. Fixtures have no origin URL, credentials or live fallback.

The guest uses its existing loopback managed relay:
`GREGALE_OUTBOUND_URL/i/{integration}/{path}`. vmmd checks its live instance,
installed containment run and retained simulation enrollment before minting
`gregale:rehearsal:relay:{run_id}`. Guest identity requests cannot mint this
audience. App events, federation and the live outbound relay remain refused.
Older outboundd implementations reject this audience during the preamble
handshake. New listeners put verified run identity in connection context; guest
headers cannot change it. Routing selects simulation before readiness, AI,
integration or provider forwarding, and missing simulator wiring refuses it.
Simulation routing also precedes ordinary URL tracing. Its dedicated spans omit
request URL and header attributes, including refused query values.

Every simulated request checks current account/app/run membership, preview
expiry, retained policy, and a live instance row. Sealing, request admission and
completion share the run advisory lock and app locks. Admission stores a pending
decision before releasing a fixture response. Completion commits before the
response or injected connection fault. Namespace cleanup retains the plan,
failure marker and sealed evidence; physical app/account purge is the retention
boundary. SQL fences refuse fixture edits and late writes to sealed windows.

## Supported adapters and evidence

The HTTP adapter matches an exact logical workload, integration, method and path.
Requests are bounded JSON objects. Query strings, encoded/traversal paths,
CONNECT, upgrades and unsupported request shapes have no supported fixture.
The Resend adapter supports `POST /emails` with sender, recipients, subject and
text or HTML content. It returns the explicitly declared provider-shaped JSON;
it does not send email, implement every provider option or validate delivery.
Applications must configure their HTTP/integration client to use the relay.
Direct HTTPS, SMTP and undeclared SDK traffic remain outside simulation scope.

Each fixture has an ordered response list. The ordinal advances separately per
workload, fixture and hashed `Idempotency-Key`; without a key, it advances per
fixture. The final response repeats. Outcomes support bounded delays, HTTP
success/error responses, 429/503 with Retry-After, and timeout/disconnect faults.
A timeout fixture delays then closes the connection without an HTTP response;
the application's deadline determines whether it observes timeout or connection
closure. This evidence records the injected outcome, not a claim about what the
application observed. The simulator records attempts rather than claiming to
reproduce a provider's business idempotency or delivery semantics.

No request headers are exported. The ledger contains a digest of the canonical
full JSON request, the hashed idempotency key and only the explicitly allowlisted
top-level payload fields. Selected nested values are retained in full. Captured
content follows the same account-owned evidence boundary as managed effects;
customers should select only fields useful for comparison. Unknown routes store
bounded placeholder identity, not caller-controlled paths or query secrets.

Version 2 has scope `managed_webhooks_and_simulated_outbound`, simulation contract
version 1, the plan digest and individual attempt evidence. Comparison requires
the same scope and fixture identity. It preserves every attempt, key group,
ordinal, request digest, selected content and injected response; generated IDs
and interleaving between groups are excluded. Reports distinguish outbound
attempts from committed managed webhook intent. An extra attempt is observable;
it is not automatically evidence that a provider delivered twice.

Protocol bounds are 64 fixtures and 64 KiB canonical plan, 16 responses per
fixture, 8 KiB response bodies, 2-second delays, 64 KiB request bodies, and 256
attempts per run. Stored simulation evidence is bounded to 512 KiB, combined
captured payload to 512 KiB, and the full snapshot to 1 MiB. Overflow raises a
retained failure marker and refuses additional admission; it cannot appear as
an empty passing run.

## Settlement and rollout

Sealing waits for supported in-flight relay simulations as well as existing
managed operations, workflows and invocations. Interrupted requests make the
inventory incomplete. A crash leaving a pending attempt prevents a complete
seal; the collector's deadline fails the run. This is not a universal queue or
worker drain protocol. Recipes must wait for their application's declared
completion before sealing. Later calls are refused rather than reopening the
window. Untracked application timers, separate job VMs, standalone Runs,
PostgreSQL/object storage adapters and payment providers remain outside scope.

[ADR-1351](1351-rehearsal-recorder-sessions.md) adds durable serving-VM
recorder sessions and a host relay closing barrier. A broker or admission
storage failure after a request reaches the host leaves an unacknowledged
obligation; it cannot become an empty passing inventory. Missing owners remain
pending and failed sessions make evidence incomplete. The versioned boundary
covers host-received relay requests. Client failures before the host relay,
including an unavailable guest loopback proxy, still require recipe assertions;
the platform does not infer an attempted external action from those failures.

Upgrade outboundd along with ADR-1349's scheduler/runtime/gateway fleet before
enabling enrollment. Simulation additionally requires the default-off
`FAAS_REHEARSAL_ACTION_SIMULATION=1` on apid. Keep that flag and
`FAAS_REHEARSAL_NETWORK_CONTAINMENT` unset until native
serving-VM containment and relay qualification, lifecycle acceptance and leakcheck
pass on the dedicated x86_64 KVM host. Portable relay/HTTP, PostgreSQL and race
tests do not substitute for native acceptance. This managed workspace cannot
supply that native qualification.
Serving vmmd needs its workload-identity signer and relay target configured;
outboundd needs the matching verification keys. A qualified network boundary
alone does not establish simulation availability.
