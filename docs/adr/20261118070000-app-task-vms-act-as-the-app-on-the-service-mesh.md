# ADR-20261118070000: App task VMs call the app's services as the app

- **Status:** accepted
- **Date:** 2026-11-18
- **Amends:** ADR-230 (deployment-attached app tasks)
- **Related:** ADR-20261017201008 (contained rehearsal commands keep their
  own command identity), ADR-20261117070620 (`app exec` debug tools)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A retail-store probe on 2026-11-16 ran `gregale app exec retail-ui -- curl
http://catalog.internal/` and got `403 caller identity is unknown` from the
service proxy. The same request from the running app succeeded.

gatewayd-internal names a service-mesh caller by the source address of the
guest's connection: `LiveServiceCallersByHostIP` maps a node's host address
to the app and deployment of the live instance there. An app task VM
(`gregale app exec`, `gregale task run`, release tasks, command crons) is
admitted as `KindAppTask` and has no `instances` row. Only contained
rehearsal commands recorded their address, in `scenario_command_networks`,
and that lookup also requires an open recorder session. Every other task VM
was anonymous, so every call it made to a private service was refused.

The task VM runs the app's own deployment, artifact, environment and
bindings. Creating a task (`POST /v1/apps/{slug}/tasks`) needs MFA and the
deploy write scope, the same as deploying a release that could make any of
these calls from a serving instance.

## Decision

1. **A running app task VM is its app on the service mesh.** Calls it makes
   are authorized as the task's app and deployment, under the same service
   policy, bindings and revocations as a serving instance of that
   deployment. It is a caller only: it never becomes a routing target.
2. **schedd records the address.** After vmmd restores the task VM, schedd
   records `(task, node, host address, lease)` in `app_task_networks`, under
   the restoring lease, before the command is dispatched. A failure to record
   fails the restore (stage `network_identity`), so a command never runs
   without the identity it was promised. A trigger accepts an insert only
   from the task's current restoring lease owner, and allows one change to a
   row: retiring it.
3. **Identity ends with the task.** The caller lookup returns a task only
   while its row is active, the task is `running` under the lease that
   recorded it, the lease has not expired, cancellation was not requested and
   the app is not deleted. schedd retires the row before it asks vmmd to
   destroy the VM, so a host address vmmd frees and reuses never names the
   task's app. If schedd dies first, lease expiry ends the identity.
4. **Rehearsal commands are unchanged.** Contained commands keep their
   recorder-bound identity in `scenario_command_networks`; the new insert
   refuses them.
5. **No revocation at vmmd start.** vmmd adopts live VMs across a restart
   (PR #7105), so revoking every task identity on a node at
   start would strip a still-running task. The lease conditions in (3) cover
   a task whose VM did not survive.

## Consequences

- `gregale app exec` and task commands reach the app's private services, as
  `docker compose exec` does.
- Two callers at one address with different apps still resolve to no
  identity (`identityForHostIP`), so a stale row can deny a call but never
  attribute it to the wrong app.
- One more indexed query per uncached caller lookup.
- Migration `20261118064823889_app_task_networks.sql` is expand-only. Until
  the new schedd runs, tasks stay anonymous as before.
