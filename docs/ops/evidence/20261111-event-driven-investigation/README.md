# Event driven investigation evidence

Original regression fixtures and results for the issues in
[the findings log](../../../implementation/event-driven-investigation.md),
measured against commit `46aad2a12` on 2026-11-11. These regressions assert
desired behavior and failed on that baseline. They are retained unchanged.
The runner now uses permanent regressions when present, falling back to the
original overlays on older checkouts. See [the remediation log](../../../implementation/event-driven-remediation.md)
for the repairs and current verification. The schema-version regression now
requires an explicit version; registering a schema still rejects new
unversioned events.

## Run the API regressions

From the repository root, with Go available:

```sh
python3 docs/ops/evidence/20261111-event-driven-investigation/run_repros.py api --store memory
```

For PostgreSQL, set `DATABASE_URL` to the local test cluster and use
`api --store postgres`, or just `api` for both stores. `pgtest` creates isolated
test schemas/databases. A skipped PostgreSQL test is not verification of the
PostgreSQL path. The recorded API run used both stores without skips.

## Run the broker regressions

Start a local NATS server with JetStream on port 14222, then run:

```sh
NATS_TEST_URL=nats://127.0.0.1:14222 python3 docs/ops/evidence/20261111-event-driven-investigation/run_repros.py broker
```

The measured broker was NATS 2.11.17 (`nats:2.11-alpine` at the time).
Kafka uses a scripted reader and Redis uses miniredis; neither needs an
external broker for these reproductions. Stop the local NATS server afterward.
Permanent live NATS tests skip when `NATS_TEST_URL` is unset and fail if an
explicitly configured server cannot be reached.

## Run the additional recovery and delivery regressions

```sh
python3 docs/ops/evidence/20261111-event-driven-investigation/run_repros.py recovery --store memory
python3 docs/ops/evidence/20261111-event-driven-investigation/run_repros.py bridge
```

Use `recovery --store postgres` or `recovery` for the PostgreSQL checks.
These suites need no external Kafka, Redis or NATS server. Recovery uses the
production Kafka poller with a scripted reader, real scheduler dispatch and
both operator replay methods. The bridge suite uses actual event routing
admission, the production bridge request builder and HTTP/2 transport, and an
in-process HTTP/2 server. It includes a passing valid-identity control.

The API suite now also includes ED010 admission probes. Its saved
`appevents-repro.log` records ED005–ED008; `headers-api-repro.log` records the
four additional ED010 ingress failures. No deployed service was contacted.
Local PostgreSQL tests use databases created by `pgtest`.

## Run the routing-control and credential regressions

```sh
python3 docs/ops/evidence/20261111-event-driven-investigation/run_repros.py routing-controls --store memory
```

Use `--store postgres` or omit `--store` for both storage implementations.
This suite covers ED011–ED013. Circuit timing uses method clock parameters,
so it does not sleep for a minute or change the machine clock. Credential
checks use synthetic secrets and an egress-denied loopback destination;
no NATS server is required and no external connection is made. Password-style
credentials provide a passing redaction control. The saved output contains
six expected failing leaf cases and two passing control cases, with no skips.

## Files

- `repro_test.go.txt` and `export_test.go.txt`: ED001–ED004 broker fixtures.
- `repro.log`: five broker regression failures, including the live NATS
  consumer deletion after a 5.208-second idle gap.
- `nats_idle.go.txt` and `nats-idle.log`: an independent NATS idle probe.
- `appevents_repro_test.go.txt` and `appevents-repro.log`: API regressions
  through real Service methods and both storage implementations.
- `recovery_repro_test.go.txt` and `recovery-repro.log`: ED009, four failing
  combinations of direct/unified replay and MemStore/PostgreSQL.
- `bridge_headers_repro_test.go.txt` and `bridge-headers-repro.log`: ED010,
  real routing admission followed by bridge transport; two failing unsafe
  identities and a passing valid-identity control.
- `headers-api-repro.log`: ED010 public ingress accepts newline IDs and
  types with 202 in both stores.
- `recovery-baseline.log` and `bridge-baseline.log`: existing focused broker
  and bridge tests pass, independently of the failing audit regressions.
- `baseline-results.log`: 54 existing scheduler tests passed without skips.
- `api-baseline.log`: existing relevant API/event package suites passed.
- `routing_controls_repro_test.go.txt` and `routing-controls-repro.log`:
  ED011–ED013 through real scheduler/store methods, in both stores.
- `routing-controls-baseline.log`: six existing scheduler tests passed with
  no skips, including PostgreSQL routing recovery.
- `credential-access-baseline.log`: existing trigger credential and app access
  package suites passed.
- `run_repros.py`: selects permanent tests or builds temporary Go overlays,
  using the toolchain declared by `go.mod`. It does not edit sources.

Go fixture files use `.go.txt` so ordinary `go test ./...` does not discover
the intentionally failing investigation tests. Before adding a permanent
regression during a fix, make it independent of helpers from unrelated tests.

## Verify the repairs

`fix-*.log` files record the repaired behavior, separate from the original
baseline failures. Permanent tests cover both stores, live JetStream,
broker-independent replay after restart, migration recovery and rollback,
company isolation, schema admission, and HTTP/2 delivery. Kafka uses scripted
readers and Redis uses miniredis. These are local checks; deployment
qualification remains pending.

With a dedicated local JetStream server on port 14222 and an isolated local
PostgreSQL test cluster available through `DATABASE_URL`:

```sh
mkdir -p /tmp/gregale-event-audit
GOTOOLCHAIN=go1.26.10 make -f Makefile -f docs/ops/evidence/20261111-event-driven-investigation/remediation-checks.mk.txt \
  verify-event-state verify-event-api verify-event-scheduler verify-event-server \
  verify-event-cli verify-event-bridge verify-event-spec
```

The recipe uses the repository's bounded compiler flags and writes logs under
`/tmp/gregale-event-audit`. `verify-event-spec` checks parity and links; OpenAPI
lint runs separately with `make spec-lint`. Run the Data API template's Node
tests in a temporary copy of the template against the local PostgreSQL test
target. Keep installed Node dependencies outside the embedded CLI template.
See the [remediation record](../../../implementation/event-driven-remediation.md)
for rollout and historical-data follow-up.
