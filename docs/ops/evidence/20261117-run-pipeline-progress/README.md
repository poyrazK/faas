# Pipeline progress and failed-branch rerun qualification

Scoped qualification passed on `gregale-internal-test-1` in `gregale-prod`,
`us-east1-b`, completed 2026-11-17 Europe/Istanbul. All seven scoped lint
groups, CLI reference validation and SDK route inventory passed. The feature
remains default-off; no deployment or activation has been performed.

The implemented contract is documented in [the guide](../../../run-pipelines.md#live-progress)
and [the decision](../../../adr/20261117090000-run-pipeline-progress-and-reruns.md).

Passing behavioral checks cover:

- Durable, metadata-only progress snapshots and monotonic events, retained-tail
  gaps, future cursor rejection, unchanged-poll deduplication, scope isolation,
  copied snapshots and stale lease rejection.
- A graph with an intact producer, failed named task and successful fan-in.
  Recovery explicitly repeats the failed task and affected fan-in, hands off the
  producer's result/artifact, admits exactly two new Runs, and accounts for only
  those attempts. Reused steps cannot be admitted again.
- An unchanged failed parent, a new child receipt, erased terminal plans, output
  fingerprints, 24-hour reuse expiry, task retirement, and exact child replay
  with writers disabled. Changed repeat acknowledgements conflict.
- Real PostgreSQL transactions and mixed-version claiming: an old claim statement
  cannot acquire a progress-enabled pipeline.
- HTTP observation, SSE replay, explicit rerun acknowledgements, writer rollback,
  app-scoped Agent progress, and original thread-grant revocation.
- Pending pipeline IDs, scoped CLI commands, proxy ownership and preview release
  classification, API/schema inventory, and standalone Go client contracts.

Local verification passes TypeScript builds, four Node SDK cases, six Python SDK
cases, two Go SDK cases, nine stateless Node starter cases and Python helper lint.
All 362 handwritten Python inputs retain their pre-regeneration hashes. SQLC
regeneration matches 205 generated files, and the new migration passes the
expand/contract linter. Generated default SDK routes keep the new preview APIs
hidden; the complete APIs remain on the preview entry points.

Reruns require the original manifest and key family. Terminal plans are not kept
for recovery. Stored artifact metadata is checked before reuse; actual byte
integrity is enforced through the existing file handoff/read path. No secret or
output bytes enter the progress journal or completion manifest. Agent callback
pipelines require a new delegated invocation; the Run rerun API does not inherit
an old conversation callback.

These are scoped control-plane, storage and handler/worker recovery checks, not a
full repository test run or a new native/runtime performance qualification. There
are no guest or VM lifecycle changes. PostgreSQL tests use disposable databases
and clean up only their own PID's template/clone databases.

All 737 source and SDK files match the internal node; see [source verification](source-verification.log).
The final PostgreSQL cleanup query found zero matching qualification databases.

See [the report](report.json), [Linux result](linux-qualification.json),
[executed functional commands](commands/run.sh), [remaining static commands](commands/lint-after-functional.sh),
source/SDK manifests and retained logs. Documentation and evidence are maintained after the executable source
snapshot.
