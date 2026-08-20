# Poller Adapter Guide

How to add a new event-source (broker) adapter to `pkg/sched`. This
guide codifies the ritual that was previously scattered across
`pkg/api/trigger.go:50-69` (the 5-step comment), the gregalemanifest
validator, and the PR #993 / PR-A review cluster. If you are adding a
new `TriggerKind` value, follow all 13 steps in order. **Do not skip
step order** — the cherry-pick window discipline (schema → runtime →
observability → API → refactor) is what makes the change reviewable
and rollbackable in <100 ms.

The 13-step checklist:

1.  [Add the constant](#1-add-the-constant)
2.  [Mirror it in pkg/gregalemanifest](#2-mirror-in-pkg-gregalemanifest)
3.  [Add the wire config struct](#3-add-the-wire-config-struct)
4.  [Extend the OpenAPI enum + add the sub-schema](#4-extend-the-openapi-enum--add-the-sub-schema)
5.  [Extend validateKindConfig](#5-extend-validatekindconfig)
6.  [Extend `triggers.kind` CHECK via a new migration](#6-extend-triggerskind-check-via-a-new-migration)
7.  [Implement the poller file](#7-implement-the-poller-file)
8.  [Extend `esmSourceClosedSet` in pkg/wire/metrics.go](#8-extend-esmsourceclosedset-in-pkg-wiremetricsgo)
9.  [Extend `shardKeyFor` in pkg/sched/dispatch_triggers.go](#9-extend-shardkeyfor-in-pkg-scheddispatch_triggersgo)
10. [Unit tests](#10-unit-tests)
11. [E2E test (build-tag gated)](#11-e2e-test-build-tag-gated)
12. [CI workflow + Makefile target](#12-ci-workflow--makefile-target)
13. [Update this guide with lessons](#13-update-this-guide-with-lessons)

After all 13 steps land, the new kind is dispatch-ready: closed-vocab
checks pass, runtime pollers exist, dashboards surface the new source
label, and the cherry-pick window lets each commit land independently.

---

## 1. Add the constant

File: `pkg/api/trigger.go:62-69`.

```go
const (
    // ...existing kinds...
    TriggerKindMyNewSource TriggerKind = "my_new_source"
)
```

The string value is the literal that flows to the SQL `triggers.kind`
column, the OpenAPI enum, the Prometheus `source=` label, the manifest
validator, and the poller registry's `Kind()` return. Pin it once;
it is never renamed.

## 2. Mirror in pkg/gregalemanifest

File: `pkg/gregalemanifest/manifest.go:55-81` (TriggerKind enum) and
line ~474-512 (kind-list switch in `Manifest.Validate`).

`pkg/gregalemanifest` carries a parallel `TriggerKind` enum because
the manifest loader is a separate code path from the runtime dispatch
(they import each other indirectly through `pkg/sched`). Both surfaces
widen in lockstep.

Add the new constant with the same string value, then add the case in
the `switch t.Kind` inside `Manifest.Validate`.

## 3. Add the wire config struct

File: `pkg/api/trigger.go` (or `pkg/gregalemanifest/manifest.go` if
the struct is also used at manifest-load time).

Mirror `KafkaTriggerConfig` shape: required fields as named struct
fields with `json` tags; optional fields as `*SubConfig` pointers.
Closed-vocab enums (SASL mechanisms, RabbitMQ URL schemes, etc.)
become a typed `string` + decoder validation, NOT a Go enum, because
`pkg/api` cannot import `pkg/state`.

## 4. Extend the OpenAPI enum + add the sub-schema

Files: `api/openapi.yaml:10276-10279` and
`pkg/apid/openapi.yaml:10276-10279` (the mirror).

Add the new kind to the enum + add a `<Kind>TriggerConfig` sub-schema
after the existing `KafkaTriggerConfig`. The Config blob on the wire
stays opaque (`additionalProperties: true`); the sub-schema is a
top-level named type so SDK generators surface it as a typed object.

Run `make spec-check` and `make sdk-check` — both must be green
before this PR merges.

## 5. Extend validateKindConfig

File: `pkg/gregalemanifest/manifest.go:618-712`.

Add a new `case TriggerKindMyNewSource:` in the switch. Validate every
required field (region, arn, queue, etc.) with a typed error that
includes the trigger index (`trigger[%d]:`). Numeric ranges get
explicit `[min, max]` checks; URL schemes get `url.Parse` +
scheme allow-list checks.

## 6. Extend `triggers.kind` CHECK via a new migration

File: `migrations/NNNNN_triggers_kind_extend_<source>.sql`.

Mirrors `migrations/00219_edge_rules_kind_limit.sql`. Use the
`DROP CONSTRAINT IF EXISTS triggers_kind_check; ADD CONSTRAINT
triggers_kind_check CHECK (kind IN (...))` pattern because PG15 has
no `ADD CONSTRAINT IF NOT EXISTS`. The DOWN path mirrors the UP path
narrowly so a downgrade after `kind=my_new_source` rows exist fails
with SQLSTATE 23514 BEFORE deleting any rows.

**Slot precheck** before authoring: `git ls-tree origin/main -- migrations/`
and `gh pr list --json files --jq '.[].files.[].path'` to find any
open-PR reservation fences. Slots must be contiguous (embed_test::
TestMigrationsContiguous is unforgiving). The ADR-041 reservation
fence pattern is the recovery path for slot collisions.

## 7. Implement the poller file

File: `pkg/sched/poller_<source>.go`.

Five components, all required:

1.  **`<source>BrokerOp` interface** — minimal broker-side surface
    the Ack/Nack methods need. Tests inject stubs. Production wires
    the concrete SDK client.
2.  **`<source>Poller` struct** — `mu sync.Mutex`, `inFlight
    map[string]<Message>` keyed by broker-native handle, plus
    broker-specific connection state.
3.  **`decode<Source>Config(t)` validator** — required-field
    checks, closed-vocab guards, TLS/credential validation. Mirrors
    `decodeKafkaConfig`.
4.  **`new<Source>Poller(t)` constructor** — wires the SDK client,
    sets defaults (timeout, batch limit, retry), returns the
    interface-conforming struct.
5.  **`init()` block** calling `registerPoller("<source>", factory)`.

The `triggerSource` interface (`pkg/sched/poller.go:107-139`) is the
contract every poller satisfies. Poll returns a `PollResult` (empty
slice = idle, error = broker failure); Ack/Nack take broker-native
handles; Close releases resources.

## 8. Extend `esmSourceClosedSet` in pkg/wire/metrics.go

File: `pkg/wire/metrics.go:2550-2552`.

Pre-instantiation MUST precede the runtime commit. If the new source
emits `ObserveESMPoll(source="my_new_source", outcome="success")`
before the closed set is extended, Prometheus silently drops the
`.WithLabelValues` call and the `rate(...{source="my_new_source"})`
panel selector renders empty even on a healthy deployment.

Order matters in the closed set: keep it alphabetical or mirror
`pkg/api.TriggerKind` order so a future closed-vocab audit can diff
them line-by-line.

## 9. Extend `shardKeyFor` in pkg/sched/dispatch_triggers.go

File: `pkg/sched/dispatch_triggers.go:1066-1095`.

The `shardKeyFor` switch extracts the broker-native shard label
(`kafka` → partition, `nats` → stream name). Each new kind picks the
natural shard label:

-   kinesis       → `Metadata["shard_id"]`
-   dynamodb_streams → `Metadata["shard_id"]`
-   rabbitmq      → `Metadata["queue"]`
-   documentdb    → `_agg` (resume-token is per-record, not per-shard)
-   msk           → partition (same path as kafka)

The shard label has a 32-byte cap with `_agg` overflow (ADR-118
§Cardinality discipline). Don't return raw user-supplied strings —
sanitize.

## 10. Unit tests

File: `pkg/sched/poller_<source>_test.go`.

Mirror `poller_kafka_test.go:65-641`. Table-driven tests with a stub
broker interface; cover:

-   `TestDecode<Source>Config_*` — required-field, URL-scheme,
    numeric-range validation
-   `Test<Source>_NackPoison*` — broker poison-strategy path
-   `Test<Source>_NackBrokerError*` — broker-error path
-   `Test<Source>_AckMultiple*` — multi-id ack
-   `Test<Source>_Close*` — Close releases resources

Tests are hermetic — no Docker, no live broker. Stub the broker
interface with a recording struct that captures calls.

## 11. E2E test (build-tag gated)

File: `pkg/sched/poller_<source>_e2e_test.go`.

Build-tag: `//go:build <source>_e2e`. Skip-on-no-Docker via
`t.Skipf("FAAS_E2E_BROKER_REQUIRED", ...)` so the default `go test
./...` does not need Docker.

Where a `testcontainers-go/modules/<source>` exists (RabbitMQ),
use it. Where it doesn't (Kinesis, DynamoDB Streams), use
`testcontainers.GenericContainer` with the official local image
(`amazon/kinesis-local`, `amazon/dynamodb-local`).

## 12. CI workflow + Makefile target

Files: `.github/workflows/<source>-e2e.yml`, Makefile.

Workflow: `workflow_dispatch` only — GHA shared runners have flaky
Docker. Mirror `.github/workflows/kafka-e2e.yml`. Add a
`make e2e-<source>` target so local developers can reproduce.

## 13. Update this guide with lessons

If your PR revealed a 10th widening site, a subtle cardinality trap,
or a new failure mode, append a one-paragraph note under "Lessons
from the field" below. The guide is a living document; future
contributors benefit from your hard-won knowledge.

---

## Lessons from the field

### 2026-08-20 — Stage 2 PR-A initial widening audit

The 9-site checklist above was a low estimate. The actual tally for
the 6→11 widening was **10+ sites**:

-   `pkg/api/trigger.go` constants (site #1)
-   `pkg/gregalemanifest/manifest.go` TriggerKind enum (site #2)
-   `pkg/gregalemanifest/manifest.go` Manifest.Validate kind-list
    switch (site #3)
-   `pkg/gregalemanifest/manifest.go` supportedKindsList() error
    literal (site #3a)
-   `pkg/gregalemanifest/manifest.go` validateKindConfig cases
    (site #4)
-   `pkg/gregalemanifest/manifest_test.go` rabbitmq-pinned negative
    test (site #5) — drop or rename to a bogus kind when adding the
    kind it pinned
-   `api/openapi.yaml` + `pkg/apid/openapi.yaml` enum + sub-schemas
    (sites #6, #7)
-   `pkg/wire/metrics.go` esmSourceClosedSet (site #8)
-   `migrations/00297_triggers.sql` + new CHECK-widening migration
    (site #9)
-   `pkg/sched/dispatch_triggers.go` shardKeyFor (site #10)
-   `pkg/sched/poller.go` package doc-comment (site #11)

The audit should be repeated before any future widening.

### Hand-rolled AWS SigV4

For Kinesis and DynamoDB Streams, do NOT add `aws-sdk-go-v2` to
`go.mod`. The precedent at `pkg/logarchive/s3client.go:1-12`
explicitly rejects the SDK for ~5 MB binary growth. Use the
`pkg/awssigv4` package instead — SigV4 + AWS JSON 1.1, ~400 lines,
no transitive deps beyond the std library.

IRSA / WebIdentity / STS is NOT supported in v1 — IAM credentials
must be static (env or shared file). Defer to Stage 3.

### DocumentDB resume-token durability

The DocumentDB adapter's "Ack" semantic is architecturally different:
server commits on next poll via the resume-token. The token must be
persisted in `trigger_records.resume_token` (a new column) — without
it, a schedd crash loses the checkpoint and the change stream
restarts from the beginning, replaying already-dispatched events.

### Cross-PR slot precheck

Before authoring migrations NNNNN-NNNNN, run `git ls-tree origin/main
-- migrations/` AND `gh pr list --state=open --json files --jq
'.[].files.[].path'` to surface open-PR reservation fences. The
precheck filters fences (ADR-041) but fences still occupy slots.
Pick `main`'s highest real-slot+1, not the precheck's suggestion.
See memory `cross-pr-slot-precheck-fence-blindspot`.

### Hand-rolled SDK vs official client

Pick hand-rolled when:

-   The wire protocol is small and well-documented (AWS JSON 1.1,
    Kinesis SubscribeToShard).
-   The official client adds >1 MB of binary weight.
-   The service has <5 surface ops the poller actually uses.

Pick official client when:

-   The wire protocol is complex (AMQP, MongoDB wire protocol).
-   The official client is well-maintained and BSD/Apache licensed.
-   Connection management (reconnect, heartbeat, etc.) is
    non-trivial.

For Stage 2 PR-A: Kinesis + DynamoDB Streams = hand-rolled
(pkg/awssigv4); RabbitMQ = `amqp091-go`; DocumentDB = `mongo-driver`;
MSK = reuse `segmentio/kafka-go` with IAM auth.
