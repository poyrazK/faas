# Concurrent backend cancellation — portable evidence, 2026-10-16

Implementation: [ADR-1540](../../../adr/1540-function-cancellation-cleanup-and-lifecycle-context.md).
Procedure: [function backend concurrency acceptance](../../function-backend-concurrency-acceptance.md).
Branch: `codex/concurrent-function-postgres`, integrating PR #4768
(`2dfcbada3a1aeca997d8e0b946e48e3feb6c4eba`) with the PostgreSQL SDK work
(`b54cb5499`) at integration base `51140d2e4`.

## Results

Environment: Linux amd64 managed container, Go 1.26.9, Node v24.19.0,
Python 3.12.14, PostgreSQL 16 with native TLS and SCRAM password authentication.
The disposable SQL image was pinned to
`postgres@sha256:1a6ab3f5345eb6dbe04a1349529caabdb0ab09293a09590fad07b2246bfa4b54`.
File-mode tests used `umask 022`. The SQL interpreter received a restricted
customer credential; only the test process held fixture administration rights.

| Check | Result | Evidence |
|---|---|---|
| All runner profiles, worker pool and rootfs race suites | Pass | [runner-race.log](runner-race.log) |
| Final handler/Fetch cleanup regression and runner core race suites | Pass | [cleanup-race.log](cleanup-race.log) |
| TLS/SCRAM SQL admission, cancellation, lifecycle callbacks and worker recovery | All four phases pass | [postgres.log](postgres.log) |
| CLI function manifests, API models and image settings | Pass | [control-plane.log](control-plane.log) |
| API concurrency round-trip/bounds and event trigger handlers | Pass | [apid.log](apid.log) |
| SDK build, canonical runtime drift and packed runtime import/typecheck | Pass | [sdk-package.log](sdk-package.log) |
| Runner/rootfs lint and changed native harness lint | Pass | [lint.log](lint.log), [metal-lint.log](metal-lint.log) |
| Post-main integration: explicit request-framing allocation guard | Race tests and lint pass | [framing-race.log](framing-race.log), [framing-lint.log](framing-lint.log) |
| Docs links, ADR uniqueness and source policy checks | Pass | [docs.log](docs.log), [policies.log](policies.log) |
| New CI workflow parsed with actionlint v1.7.7 | Pass locally; remote workflow not run | [workflow.log](workflow.log) |
| Final `metal` test binary build with race instrumentation | Pass; compilation only | [native-build.log](native-build.log) |
| Strict `make test-metal` attempt | Fails native-host preflight | [native-preflight.log](native-preflight.log) |
| `make leakcheck` | Fails: native loop devices unavailable | [leakcheck.log](leakcheck.log) |

The PostgreSQL test independently witnessed TLS on the application's session
and a SCRAM hash on its generated runtime role. It blocked two writes with an
administrator-owned table lock, rejected excess SQL admission, cancelled one
HTTP request, verified neighbour continuity in the same Node process, then
released the lock and found only the healthy write. Checkpoint callbacks closed
that worker's sessions; restore callbacks changed its SQL backend PID. Killing
idle SQL sessions and killing the interpreter both recovered without replaying
the mutation. Four simultaneous context requests retained their own invocation
IDs and headers on the replacement worker.

Cancellation acknowledgement is also tested behind explicit handler and Fetch
body cleanup gates. A neighbour finishes while cleanup is blocked. Only after
release does the acknowledgement arrive; cancelled response frames stay
suppressed. A separate pool test fills all worker slots and proves cancellation
does not free capacity early.

After testing, SQL witnesses found **zero** fixture roles and **zero** private
fixture databases. The owned container, anonymous data volume, certificate key
and temporary credential files were removed.

Before publishing PR #4768, the branch incorporated `main` at `4ba08b34e`.
Node and Python SDK regeneration matched the combined OpenAPI spec. The review's
allocation warning prompted an explicit `math.MaxInt` guard over the full request
frame, including its generated ID; the worker-pool race suite and lint passed
again. Hosted CI results are recorded on the pull request's published revision.

## Qualification boundary

This environment is non-root and has no `/dev/kvm`; `/dev/loop0`–`/dev/loop7`
are unavailable. The strict native suite failed preflight instead of skipping
to a passing result. No supported native VM, snapshot, frozen-thaw or native
performance result is claimed. Source hashes record the reviewed files; they
are not an executable provenance or rollout approval bundle.

The real SQL lifecycle calls here are simulated guest-init HTTP calls in one
interpreter. They do not qualify VM-wide pool draining across multiple workers,
native credential delivery/rotation, or live snapshot/thaw recovery. The new CI
job selects Node 22 and Python 3.12; its hosted execution remains pending.
The generated Node `waitUntil` scaffold is outside this completion fence and
does not establish durable post-response work. Supported native acceptance is
required before production qualification or throughput/RSS claims against
Vercel.
