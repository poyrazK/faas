# ADR-1577: Map migration runtime variables to selected services

## Status

Accepted for local implementation.

## Context

Repository dependencies produce `GREGALE_SERVICE_<NAME>_URL` discovery variables,
but existing applications often read names such as `API_URL`. Customers still
have to construct private URLs or edit code after reviewing app allocations.
Those manual changes are easy to lose while retrying a partial migration.

## Decision

`CreateProjectMigrationRequest.service_connections` maps exact selected caller
names to runtime variable-to-service choices. Each target must be a declared
dependency and another selected source-backed HTTP/GraphQL service. Each caller
can map at most ten keys. Empty/omitted mappings retain supplied existing values.
Mapping a key and supplying its value in `initial_secrets` is rejected. Platform
settings and conventional public browser/build-time prefixes are excluded.

The existing migration JSON journal saves only variable and logical service
names alongside deployment identities. No new table or intent writer is added.
The create digest includes these choices; resume cannot replace them. Before
reconciliation or builds, the migration domain reconstructs the HTTPS companion
URL through `api.ServiceBindingEnvForTransport`. The existing project setup
owner validates aggregate secret quotas and seals every selected caller's
configuration before queueing any build. Mappings require secret write scope.
The URL is `https://<logical-service>.internal`, independent of global app slugs
and the caller's canonical discovery transport. Later deployments retain it
through the normal secret lifecycle. Removing a mapping requires configuring
the app separately or creating another migration.

The console offers explicit choices for detected, required, imported or manually
named runtime keys. It defaults to keeping existing connections and excludes
bound keys' pasted values from submission. Required keys can be satisfied by
these explicit mappings. Saved progress displays choices and automatically
restores them on retry. Unselected dependencies, databases, storage and public
browser/build-time settings retain their own configuration paths.

Partial migration scans retain unselected source services as reference-only graph
metadata. They do not create, update, remove or build those services. Reconcile
preserves discovery bindings only for unambiguous existing production members of
the same creator/project. References still on another provider gain no internal
route. Healthy dependency conditions require a selected service or an existing
member; unknown references and job health gates retain their validation errors.

Migration dependency checks accept at most ten `service_connections` selectors,
each naming a runtime key, logical service and canonical target deployment UUID.
Guided migration verification waits for connected targets to become live and
includes the journal's exact target deployments automatically. APID validates
same creator/project production membership, declared bindings, target scope and
the selected live deployment with all traffic. It repeats those checks during
the run and whenever cutover consumes passed evidence. A replacement target
blocks the previous evidence. A report omitting the selected connections is not
offered as cutover evidence by the guided console.

The dependency task runs in the exact caller deployment. It requires the custom
key to equal the canonical private HTTPS value and the corresponding generated
HTTPS discovery key to be present. It reuses the existing verified TLS service
probe with the selected target UUID. The platform HEAD probe validates identity,
authorization and healthy endpoint routing; it does not execute an application
request or wake the target. Startup and optional after-wake tasks require a
complete ordered report for every selected connection. Older guests omitting
new checks fail closed. Task timeouts include a bounded allowance per connection.
Only verdicts and reconstructed diagnostics enter durable reports.

## Consequences

Customers can keep their existing server-side variable names without repository
edits or hand-built service URLs. Retries preserve choices and accepted siblings.
Configuration values remain sealed and subject to normal quotas, revisions and
runtime refresh. Services still need valid private CA trust and caller/target
authorization. The probe proves configured private routing to the selected
deployment; customers should also choose an application smoke path that exercises
their actual dependency use. Public/build-time URL provisioning remains separate.

## Validation

Tests cover invalid or unselected targets, unsupported keys, conflicting values,
saved mapping retries, pre-build configuration, partial retries with accepted
siblings, exact guest output coverage, sanitization, owned project targets,
replacement deployment invalidation, PostgreSQL restart persistence, generated
SDK wire shapes and console selection/resume behavior. Native x86_64 KVM lifecycle
acceptance remains required before shipping the complete migration workflow.
