# Pipeline schema prerequisite — 2026-11-15

This extracts the byte-identical expand migrations from PRs #7252 and #7264
so the schema can land before their capable API and worker binaries. The three
new JSON columns default to empty objects. Existing CDC, version 1 serving and
operability claims retain their protocol support. Explicit evolution and work
configuration requires the corresponding newer protocol. The ADRs describe the
later application changes; this prerequisite exposes no new API or CLI controls.
Designs: [evolution](../../../adr/20261113205500-pipeline-serving-evolution.md) and
[scheduled work](../../../adr/20261114083000-pipeline-scheduled-imports-quality-and-work-budgets.md).

A fresh disposable PostgreSQL 16 database replayed every migration and produced
the canonical 111,770-line schema and migration checksum. SQLC generation and
ADR uniqueness passed.

[Compatibility SQL](legacy-worker-compat.sql) invokes the actual migrated trigger
functions against temporary copies of the pipeline/alert column shapes as an
unprivileged runtime-role fixture. It verifies legacy CDC/serving/index claims
and ordinary monitoring, refuses unsupported evolution/scheduled claims and
scheduled observations, and accepts the capable protocols. The transaction,
including the role fixture and temporary relations, rolls back.
[Recorded output](legacy-worker-compat.txt).

The hosted native pipeline suite remains the current-binary/expanded-schema
integration gate. This is disposable-fixture evidence; live provider
qualification and production enablement remain separate work.
