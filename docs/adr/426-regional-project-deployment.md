# ADR-426: Regional project deployment

Date: 2026-10-02

Status: Internal implementation; native fleet qualification pending.

## Context

ADR-424 fences app execution to an immutable compute region. ADR-425 adds
durable discovery and new-app admission policy. Project scan/apply and GitHub
reconciliation must carry the same placement contract, including services
added after the first deployment. A transient CLI flag would leave later
services unconstrained.

## Decision

Persist an immutable `projects.compute_region` default. Empty preserves legacy
unconstrained placement. `gregale scan --region REGION` and `gregale deploy
--project --region REGION` select the default when creating a project. Omitting
the flag on an existing project resolves its durable default. Supplying a
different default returns a conflict before mutation; create a new project to
change it. Existing projects retain an empty default and are not repinned.

Individual source workloads may declare `x-gregale-region` on a Compose build
service or `compute_region` on an app.yaml service, worker, or job. An explicit
workload declaration takes precedence over the project default. The default
applies only to new members. Omission on an existing member preserves that
app's persisted region, including an empty legacy choice. A declaration that
would change an existing app's region makes the plan inapplicable.

Plans return the effective project default and each workload's effective
`compute_region`. The plan identity token binds the resolved default alongside
the source digest, account, binding, environment, and ingress-policy inputs.
Apply re-scans the uploaded source and rechecks current admission before any
project or workload mutation. The token remains the existing base64 identity
envelope; it is not a signed capacity reservation. Protected environments
retain their exact-token approval requirement.

New regional projects and apps consult the durable registry. PostgreSQL
serializes project creation with region-policy updates and refuses changes to
the project default. Region deletion is refused while a project references it,
even before that project has apps. A down migration refuses while regional
projects remain. Workload app inserts retain ADR-425's transactional admission
guard. Atomic reconcile keeps app changes, removals, creates, and cron updates
together if a policy change races the preflight.

Both apid and githubd use the shared region admission preflight. Each daemon's
`regional_compute_enabled` operator gate defaults to false. Enable it on both
only for a qualified fleet. Turning a gate off or pausing new-app admission
continues to permit existing regional workload updates and deployment. New
regional members are refused until admission is enabled again.

GitHub reconciliation resolves the stored project default for new services and
preserves existing member regions. Pull-request previews retain their parent
app's region. Project environment cloning reuses the same workload app
identities; it copies scoped configuration rather than relocating execution.
Environment selection does not create a separate regional placement target.

The REST source-upload and source-ref planners, Go clients, generated Node and
Python clients, CLI previews, project reads, and cached dashboard apply carry
the placement fields. Local retry keys include the requested compute region.

## Boundaries

This extends compute placement inside the shared fleet. Project dependencies
can span regions and continue to use existing service routing; neither network
latency nor regional managed-data placement is promised. Region declarations
on external image-only Compose managed resources are rejected rather than
suggesting that Gregale relocates them.

Standalone managed Jobs, independent regional control planes and artifact/data
stores, geographic ingress, regional fault isolation, and a global app with
multiple regional deployment targets still require further implementation.
The end-to-end multi-region goal remains incomplete until its runtime and
operational acceptance evidence is available.

## Validation and rollout

Apply the migration, upgrade apid/githubd and clients, and configure the same
qualified admission gate on both daemons. Keep scheduling enforcement from
ADR-424 on every compute host. No additional quota or capacity reservation is
introduced.

```sh
gregale regions
gregale scan --path . --project-slug shop --region eu-fsn
gregale deploy --project --path . --project-slug shop --region eu-fsn --yes
```

Portable coverage checks source declarations, effective defaults, immutable
conflicts, token mismatches, zero writes for blocked plans, pause/outage
diagnostics, preservation on redeploy and environment clone, CLI forwarding,
and SDK multipart identity. PostgreSQL coverage exercises persistence and
direct SQL project identity/admission guards. Runtime qualification remains the
native Linux x86_64/KVM acceptance in ADR-425 plus project deployment under that
same labeled fleet; portable tests do not provide this evidence.
