# ADR-20261019120043: Atomic HTTP service additions

## Status

Implemented behind `FAAS_ATOMIC_SERVICE_ADDITIONS_ENABLED=1`. Admission defaults
off pending dedicated native qualification. Control-plane tests do not qualify
guest startup, edge routing or native recovery.

## Context

Atomic source deployment previously required exactly the existing app inventory.
Ordinary reconciliation creates new apps before builds finish. Applying it to an
atomic release would change publication completeness and make rollback to the
smaller baseline fail, even when every old deployment remains healthy.

## Decision

`gregale deploy --project --atomic --add-services` explicitly requests additions.
Scan and apply carry `Project-Add-Services: required`; the client must observe
the planned additions before applying. `deploy check` understands the same
option without uploading source or reserving apps.

The first version permits independent HTTP source services. Existing serving
membership and source-owned settings must remain unchanged. Removal, images,
workers, schedules, dependencies, declared environment keys, operation changes,
migrations, configuration snapshots and progressive rollout are unsupported in
this combination. Services use normal plan defaults and inherit the baseline's
revision-retention duration.

Planning is read-only. Source archive identity, baseline and canonical settings
fingerprints bind added names and roots to the plan. App IDs are assigned only
during admission. Private `project_release_additions` records reserve the apps
for a durable submission under account quota and project locks. The entire batch
commits or rolls back. Reservations consume deployed-app quota. An unrelated
deployment cannot take over a dormant reservation.

Reservation records and the receipt's concrete app IDs commit together, so a
restart between reservation and build selection preserves recoverable ownership.
Reserved apps are audited and announced through the normal placement notification
path after admission.

Every service builds at explicit zero traffic and must finish readiness before
publication. The active baseline excludes new services, so default public ingress
and baseline service calls fail closed for those apps. Dark candidates have no
direct revision pin. Removing the active graph while a reservation is dormant is
rejected so weighted fallback cannot expose it.

Publication checks captured addition identities, settings, owner, complete
serving membership and active baseline. Graph activation and transition of the
reservations to serving membership share a transaction. An ordinary publisher
may omit dormant reservations but cannot activate them. Unrelated, unregistered
apps still participate in the full-membership check.

With `--verify`, existing critical-route probes retain their real baseline and
candidate evidence. New services pass startup readiness; this version does not
invent baseline probe evidence for a service that has never served. Regressions
restore the exact original graph and return only the attempt-owned additions to
dormant reservations in the same transaction. The expanded graph and its service
deployments remain reachable through their retained graph until normal expiry.

Failed and withdrawn additions remain visible app reservations with no default
route. They do not prevent later releases of the serving graph. A new attempt
may reuse the same app IDs and settings once the previous owner is terminal.
Changing reserved settings requires deleting the unused reservation through the
existing app lifecycle and planning again. There is no automatic hard deletion.

## Validation and rollout

Memory and PostgreSQL contracts cover publication, missing candidates, readiness,
settings drift, competing publication, quota, retry ownership and restart from a
persisted rollback checkpoint. HTTP and CLI contracts check read-only scans,
explicit opt-in, stale plans and server acknowledgement.

Before enabling admission on dedicated x86_64 KVM hardware:

1. Deploy a baseline HTTP service and plan a new independent HTTP service.
2. Confirm default and direct revision ingress reject the candidate before
   cutover. Confirm successful readiness publishes both members together.
3. Fail startup and verify the baseline remains active. Cause a real critical
   route regression with `--verify` and confirm original membership is restored.
4. Restart APID during reservation, build, publication and rollback; resume each
   durable receipt. Keep a client pinned to the expanded graph through rollback
   and confirm service calls work until expiry.
5. Exercise quota rejection and competing publication. Retain receipt IDs, graph
   IDs, readiness evidence and edge observations before enabling the flag.

## Consequences

Project topology can grow through the atomic source path while preserving exact
rollback membership. Dormant reservations distinguish incomplete additions from
unexpected inventory drift. The first version requires spare app quota and
leaves service removal and configuration-aware topology changes to later work.
