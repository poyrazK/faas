# ADR-433: Customer automation drafts and publication

Status: Accepted (preview)

## Context

Gregale already executes immutable workflow run snapshots. Scheduled and event
starts previously read only definitions carried by the live default deployment.
Customers need to create and update those definitions through an API without
redeploying application code. YAML deployment and API publication must not
silently overwrite each other.

## Decision

`workflow_automation_definitions` stores one draft and an optional published
snapshot per app and automation name. Saving a draft permits semantic errors;
publishing validates the DAG against the current plan and requires a live default
deployment. Publication changes only future admissions. In-flight runs and events
accepted before publication retain their snapshots.

Ownership is per name. Until the first API publication, deployed YAML owns
that name, including when a saved draft exists. Publishing a YAML name
requires `take_over_manifest: true`. Once published, the API definition
survives later deployments. Deleting an API publication requires
`restore_manifest: true` when live YAML defines the same name, and returns that
name to YAML. Deleting a draft alone leaves its YAML publication active.

`app_workflow_definitions` resolves the union of live YAML and API
publications for PostgreSQL schedules and event acceptance. The memory store and
manual-run API use the same precedence. Automatic pause is stored independently
of the published snapshot, so subsequent publication preserves pause. Initial
publication respects a draft trigger's `enabled` value. Manual starts remain
available while automatic starts are paused; pause and deletion do not cancel
existing runs or retract events already accepted.

Every write supplies `expected_version`. Zero creates a draft; stale revisions
return `automation_version_conflict`. A database sequence assigns revisions so
removal and recreation cannot reuse an old revision (avoiding an ABA overwrite).
Versions are opaque revisions, not consecutive counters for an individual name.
Only the latest draft and published revision are stored; full edit history and
rollback are deferred. Run definition snapshots continue to provide execution
history.

Drafts, publications, and live YAML names share `WorkflowMaxPerApp`, counting each
name once. Authoring mutations and deployment creation/promotion use the same app
advisory admission lock. Promotion checks quotas again because drafts may change
while a build is pending. Definition bytes are bounded centrally at 1 MiB.

## API

- `GET /v1/apps/{slug}/automations` lists drafts, publications, YAML ownership,
  plan limits, and runtime availability.
- `GET /v1/apps/{slug}/automations/{name}` reads one definition.
- `PUT /v1/apps/{slug}/automations/{name}` saves a draft.
- `POST /v1/apps/{slug}/automations:validate` checks a definition without executing
  or persisting it and returns a step order and nominal next scheduled occurrence.
- `POST /v1/apps/{slug}/automations/{name}/publish` validates and publishes the
  saved draft; idempotency protects retries.
- `PUT /v1/apps/{slug}/automations/{name}/enabled` pauses/resumes automatic starts.
- `DELETE /v1/apps/{slug}/automations/{name}?expected_version=...` removes the
  saved definition with the explicit YAML restoration option when necessary.

Read and validation routes require the existing read surface scope. Mutations
require the deployment write surface scope. Account ownership, MFA, rate limits,
and normal authentication apply to every route.

This change includes backend authoring APIs and generated SDKs only. No frontend
changes are included. The existing manual-run and run/step inspection APIs use
the published definition and immutable execution snapshots. Sample runs execute
real app side effects. A dashboard editor, connector marketplace, and complete
revision history are future work. The wire source value `dashboard` identifies
API-managed publications; it does not imply a shipped frontend editor.

## Rollout

Apply migrations on apid and deploy schedd with the effective-definition query
before exposing authoring to customers. `FAAS_WORKFLOWS_ENABLED=1` is required on
both apid and schedd, with the existing gateway executor configuration and Hobby
or higher.
The preview gate stays unchanged; saving intent does not enable execution.
Publication, pause/resume, or deletion clears that name's schedule cursor in the
same transaction. The scheduler re-arms it at the next evaluation and does not
catch up missed minutes. Existing scheduled runs continue.

Removing the authoring migration discards saved drafts and publications and
returns all names to YAML, so migration rollback requires exporting definitions
first. No production migration or runtime gate is changed by this code change.
