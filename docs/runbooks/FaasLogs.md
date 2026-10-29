# Customer logs

Operational owner: the gateway owner (gatewayd-internal: the customer logs
route `cmd/gatewayd-internal/app_logs.go`, the archive read
`cmd/gatewayd-internal/app_logs_archive.go` and drain delivery
`cmd/gatewayd-internal/log_drains.go`), with vmmd for the instance log rings
and the archive shipper (`pkg/fcvm/logbuf`, `cmd/vmmd/log_archive.go`,
`pkg/logarchive`), schedd for the log stream (`pkg/sched/logs.go`) and apid
for the dashboard page, the drain API and archive erasure
(`pkg/apid/logerasure`). Pages go to `faas-platform-oncall`
([escalation](../ops/escalation.md)). Capabilities: `logs` (ga) and
`log-drains` (beta, parent of `datadog-integration`). Customer docs:
[Logs](../logs.md).

Alerts with their own runbooks:
[FaasLogArchiveShipperDegraded](FaasLogArchiveShipperDegraded.md) (also
`FaasLogArchiveSpoolNearCap`),
[FaasCustomerLogDrainDegraded](FaasCustomerLogDrainDegraded.md) (the four
`FaasCustomerLogDrain*` alerts) and
[FaasAccountErasureDeferred](FaasAccountErasureDeferred.md). This page
covers how logs flow, the kill switches, rollback and recovery.

## How logs flow

1. The guest's serial console carries the app's stdout and stderr to vmmd,
   which keeps each instance's latest lines in a 10 MiB ring.
2. `GET /v1/apps/{slug}/logs` is served by gatewayd-internal, never apid: it
   authenticates the caller, resolves the app, and streams from the owner
   node's schedd (`StreamAppLogs`), which reads vmmd's rings. The dashboard's
   Logs tab and `gregale logs` use the same route.
3. When an instance stops, or a line falls out of a full ring, vmmd spools
   it locally and ships the day's file to the archive bucket every five
   minutes, keyed `faas-logs/{instance}/{YYYY}/{MM}/{DD}.jsonl.gz`.
4. `?archive=1&instance=…&date=…` streams that object back through
   gatewayd-internal after checking the instance belongs to the app and the
   date is inside the plan's retention (1/7/30/90 days).
5. Log drains: the gatewayd-internal on the app's owner node follows the same
   stream for each enabled drain and delivers batches to the customer's URL.
6. The bucket's lifecycle rule deletes objects 90 days after they are
   written; account erasure deletes an account's instances' prefixes before
   the account row goes.

The request path never depends on logs: a ring, schedd stream, archive or
drain failure loses or delays log lines, never requests.

## Release and rollback

- **Hide logs from everyone:**
  `gregalectl flags disable --key logs --reason <incident> --yes`. Within
  seconds apid hides the Logs tab and `/v1/deployments/{id}/logs`,
  gatewayd-internal answers `GET /v1/apps/{slug}/logs` (live and archive) as
  an unknown route, and the CLI hides `gregale logs`. Collection does not
  stop: rings keep filling and the archive keeps shipping, so
  `gregalectl flags enable --key logs` brings back everything still retained.
- **Hide logs from one account:**
  `gregalectl flags deny --key logs --account-id <uuid> --reason <why>`.
- **Stop all log drains:**
  `gregalectl flags disable --key log_drains --reason <incident> --yes`. apid
  hides the drain API and pages, and each gatewayd-internal stops its drain
  workers at the next reconcile (30 s). Drain rows and their health stay.
  On re-enable each drain resumes from what is still in the instance rings;
  lines that left the rings while drains were off are not forwarded (they
  are still in the archive). The Datadog integration stops with it.
- **Stop one account's drains:**
  `gregalectl flags deny --key log_drains --account-id <uuid> --reason <why>`.
  To stop one drain, pause it as the customer would
  (`gregale logs drain update … --disable`) and tell them.
- gatewayd-internal decides these flags by account only, so rules that
  target plans or organizations do not apply to the logs route or drain
  delivery.
- **Code rollback:** gatewayd-internal, schedd, vmmd and apid release
  independently ([release compatibility](../release-compatibility.md)); roll
  back the unit that introduced the regression. Archive objects and the
  spool format are unchanged by a rollback within one release.

## Recovery

### Live logs empty or failing for every app

1. Confirm the route answers at all: `gregale logs <app>` on a known-busy app
   prints lines, an `archive gap` frame or a problem code.
2. `app_logs_unavailable` or a stream that ends at once: check the owner
   schedd is up (`FaasDaemonDown`, `FaasDaemonNotReady`) and gatewayd-internal's
   logs for `StreamAppLogs` errors.
3. Lines missing only on one node: check that node's vmmd. A vmmd crash or
   restart loses the lines held only in its rings; archived lines are safe.

### Archived logs unavailable

1. `log_archive_unconfigured` (503): the node has no archive bucket. Check
   `/etc/faas/secrets/storage-box/archive-creds.json` on the node and the
   `FAAS_LOG_ARCHIVE_*` settings in the daemon's environment; re-run the
   `log_archive` Ansible role with the manifest's archive settings.
2. Reads fail but writes succeed, or the reverse: follow
   [FaasLogArchiveShipperDegraded](FaasLogArchiveShipperDegraded.md).
3. While the bucket is unreachable the spool fills up to
   `FAAS_LOG_ARCHIVE_LOCAL_BYTES_MAX` (10 GiB); past it new archive lines are
   dropped. Fixing the bucket drains the spool on the next flush.

### Node loss

Lines in a lost node's rings that were not yet archived are lost; archived
days are unaffected because the bucket is off-node. Customers who need every
line use a log drain ([Logs](../logs.md#retention) says so).

### Bucket loss or wrong bucket

The archive is the only copy of parked instances' logs. If the bucket is
deleted or the nodes point at the wrong one, readers see `archive gap` for
past days and new days land in the wrong place. Point every node back at the
right bucket (manifest, then the `log_archive` role); objects written to a
wrong bucket can be copied across with the same keys. Soft delete keeps
deleted objects for 7 days.
