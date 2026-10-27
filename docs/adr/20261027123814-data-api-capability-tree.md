# ADR: The Data API is a managed PostgreSQL sub-feature and owns its contract tasks

Date: 2026-10-27
Status: Accepted
Related: [ADR-650](650-schema-generated-data-api.md), [ADR-968](968-data-api-projects-and-owner-workspace.md), [ADR 20261023115741](20261023115741-every-capability-behind-a-platform-flag.md) (capability flags), [ADR-230](230-deployment-attached-app-tasks.md) (app tasks)

## Context

Product capability flags (ADR 20261023115741) release each capability on its
own, and a sub-feature is released only while its parent is. Four gaps let
the Data API's release drift from what it depends on:

1. `data-api` had no parent. Every Data API serves a managed PostgreSQL
   database through a `data_api` binding, yet turning `managed-postgres` off
   left the Data API routes, dashboard and CLI released.
2. A `data_api` binding could be created, through the binding API or a
   `gregale.yaml` database dependency, for an account the Data API was not
   released to. The binding is what makes an app a Data API.
3. The CLI generated types, validators and contract snapshots with the
   general app-task API (`/v1/apps/{slug}/tasks`), which belongs to the
   separately released `app-tasks` capability. An account with the Data API
   but not app tasks could not run `data-api types`, `diff`, `check`, `sync`
   or `clients`.
4. A data pipeline could create a Data API app without the capability, and
   configured it without the snapshot lifecycle hooks the CLI sets.

## Decision

1. **Parent.** `data-api` is a child of `managed-postgres` in
   `pkg/productcap/catalog.json`; the flag decider already walks ancestors, so
   the Data API is released only while managed PostgreSQL is.
2. **Binding check.** Creating a binding with `access: data_api` requires the
   `data-api` capability for the subject the request is decided for (the app's
   organization for the binding API, the manifest's ownership for
   `gregale.yaml`). Otherwise it answers `403 capability_not_released`, naming
   the setting. Other access modes, and bindings copied by environment clones
   or GitOps qualification from an existing one, are unchanged.
3. **Contract tasks.** `POST /v1/apps/{slug}/data-api/contract-tasks` admits
   one fixed inspection, `{"kind": "types" | "validators" | "schema"}`, as an
   ordinary app task (same deployment pinning, limits, VM and receipt), and
   `GET /v1/apps/{slug}/data-api/contract-tasks/{id}` reads it back; any other
   app task is not found there. The routes sit under the Data API's route
   prefix, so the Data API capability, not `app-tasks`, decides them. The
   commands come from one table in `pkg/api` shared with the dashboard, so a
   caller cannot run anything else through them. The host's app-task executor
   switch (`requireAppTaskAPI`) still applies: without an executor nothing can
   run.
4. **Skew.** The CLI uses the new routes and, when apid answers "no such API
   route" (an older apid, release N-1), creates the same command as an app
   task and polls it there.
5. **Data pipelines.** A pipeline setup that creates a Data API app needs the
   `data-api` capability for the account, like the CLI, and the app it
   configures registers the runtime's snapshot lifecycle hooks
   (ADR 20261026131131), which only the CLI registered before: a pipeline's
   Data API snapshot could hold SQL sessions.

## Consequences

- Turning `managed-postgres` off hides the Data API as well.
- Data API owner tooling no longer needs `app-tasks` released to the account.
- Contract tasks still appear in `GET /v1/apps/{slug}/tasks` for accounts that
  have app tasks, because they are app tasks.
- A capability-hidden contract-task route also reads "no such API route", so
  the CLI falls back to the app-task route; when that is hidden too, the
  caller sees the app-task route's 404.
