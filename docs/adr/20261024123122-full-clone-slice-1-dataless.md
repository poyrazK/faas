# ADR-20261024123122: Full environment clones, slice 1 — data-less clones end to end

Date: 2026-10-24
Status: Accepted
Amends: ADR-590 (complete project-environment clones), ADR-20261023091049 (slice 0)

## Context

Slice 0 measured the clone gap per table and switched the dormant machinery
off. A full clone still could not happen for any project:

- **Admission counted every table.** A configuration table blocked admission
  until it had all six capabilities, whether or not the source used it. Only 5
  of 213 configuration tables qualified, so every project was refused, even
  one with a single HTTP app.
- **Publication refused every capture.** Work policies and queue bindings are
  application-wide, so the publication guard refused even an empty work or
  queue catalogue: a policy production added later might reach the stage.
- **Nothing could fail.** No code moved an operation to failed or compensating.
  A clone whose target deployment failed retried forever, kept its half-built
  target and held the target slug.
- **The handler always answered 409.**

## Decision

1. **Clone admission counts only what the source uses.** Every configuration
   and customer-data entry in the clone schema registry declares, under
   `clone`, who owns its rows. The owner is the source environment (by ID, or
   by slug beside `project_id`), the project, the captured workloads or the
   captured deployments. A table can reach its owner through up to three
   foreign-key hops. The account, org or platform own configuration outside
   the environment boundary: every environment shares it, and the clone
   neither copies nor isolates it. The registry rejects a configuration or
   data table without a declaration.

   The capture transaction calls `project_environment_clone_inventory_present`
   with those declarations. Because it runs in the capture's repeatable-read
   snapshot, the inventory and the captured configuration agree. With
   `RequireCompleteInventory`, which public admission sets, a used table
   blocks admission, named in the 409, if it:
   - lacks `capture` or `materialize` (`clone_strategy_unavailable`);
   - is customer data (`isolated_data_strategy_unavailable`);
   - has a strategy that cannot run yet: managed PostgreSQL bindings and object
     buckets need the data checkpoint (`data_checkpoint_unavailable`); app work
     policies and queue bindings need activation (`work_activation_unavailable`).

   No operation exists after a refusal. Clone admission needs only `capture`
   and `materialize`. Diff, qualification, promotion and rollback stay
   requirements of full promotion, and the coverage report keeps measuring all
   six.

2. **Deliberate non-copies are declared.** `strategy` marks a table the clone
   does not copy:
   - `shared`: the project row, org application-standard enrollment, and
     account, org and platform configuration;
   - `derived`: the target environment row and its workload heads, and the
     route-policy and OpenAPI snapshots a target deployment records when it
     goes live;
   - `target_identity`: custom and default domains, because the stage gets its
     own generated URL.

   `apps` gains `capture` and `materialize`. Every `App` field has a clone
   policy, and deployments resolve copied fields from their pinned workload
   specification. Its other columns are git binding, revision bookkeeping and
   operational state.

3. **Explicitly empty work and queue catalogues publish.** Production
   producers resolve only to the production lane: events, crons, broker and
   realtime triggers, unbound and production queues, inbound webhooks,
   workflows, replay and API invoke. Work that starts on the stage never reads
   application work policies. So a policy production adds later cannot reach a
   published stage. A captured policy, producer binding, queue binding or
   environment work policy still blocks publication, and admission refuses it
   first. With empty catalogues there is nothing on the target to compare, so
   routes and edge rules carry the publication proof.

4. **A data-less clone fails cleanly.** In copying or publishing, a data-less
   operation goes straight to `compensating` with an error code when:
   - a target deployment fails (`target_deployment_failed`); or
   - it has not published 30 minutes after creation
     (`clone_deadline_exceeded`).

   Compensation then:
   1. cancels the target deployments the clone created, notifying schedd,
      which drains their instances;
   2. retries until none of their instances is active;
   3. in one transaction, removes the target environment's values,
      configuration, approvals, route and edge policies and environment row,
      which cascades to its workload specifications, heads and flags;
   4. moves the operation to `compensated`.

   That frees the target slug and drops the gateway's not-ready gate. The
   cancelled deployments remain as history, as after any environment delete.
   Data-bearing compensation is unchanged and still needs native retirement
   proofs.

5. **Admission has its own switch.** `POST
   /v1/projects/{slug}/environment-clones` admits only when
   `full_environment_clones_enabled` (the machinery, slice 0) and the apid
   setting `full_environment_clone_admission_enabled` are both on. Turn on the
   second only after every daemon has an apply receipt for the first;
   otherwise a gateway could serve a stage before its clone publishes. Once
   open, the handler captures the source under the request's Idempotency-Key
   and answers 202 with the environment and its durable operation. The CLI
   keeps `--full` dark behind `FAAS_FULL_ENVIRONMENT_CLONES_ENABLED`.
   `protected: true` is refused; protect the stage once it is ready.

6. **Data-less clones never need the configuration write fence.** Only the
   data checkpoint takes a configuration guard. The data-less path captures in
   one repeatable-read transaction and materializes from that frozen capture.
   This amends slice 0's note that opening admission enables the fence first:
   only data-bearing admission does.

## Consequences

- A project of HTTP workloads with values and secrets, deployed through the
  ordinary paths and using no strategy-less feature (crons, alerts, edge rules,
  queues, events, managed data and so on), can be cloned in one command: the
  stage runs the source's artifacts with its own values, and stage edits do not
  reach production. Each newly supported table widens what admission accepts.
- A refused admission names every blocking table, so the gap a customer hits
  is visible and measurable.
- Admission costs one existence probe per declared table inside the capture
  transaction.
- A new configuration or data table must declare its owner, or the registry
  fails to load.
- Main's #5275 had renamed `deploy_tokens.token_sha256` to `token_digest` in
  the registry, which failed every PostgreSQL clone capture. This change
  restores it.

## Rejected alternatives

- **Probing every table's rows from Go with generated queries.** That needs one
  query per table, all regenerated whenever a table is added. A single function
  driven by the registry keeps the declaration and the probe together.
- **Treating account-level configuration as a blocker when present.** Nearly
  every account has some, and an environment clone cannot isolate it in any
  case.
- **An HTTP-only runtime lane for full-clone stages.** Production producers
  already resolve only to production, and refusing stage-origin work (async
  routes, durable entities) would make a full-clone stage behave differently
  from a partial one.

## Amendment 1 (2026-10-31): blockers found before admission opens

A code audit of fully copyable stages (2026-10-30) found gaps that would bite
once admission opens. All are fixed together; none changes what a clone copies.

- **The inventory counts only rows a clone would copy.** A registry entry may
  name `live` conditions on the probed table itself: a column that must be
  NULL (`retired_at`, `revoked_at`, `deleted_at`), a state it must not hold
  (`deleted`), or a scope that must be the source environment's. Deleted
  buckets, retired queue bindings, revoked credentials and other
  environments' rows no longer block a clone forever. The probe function
  validates each condition, and the registry loader checks every named column
  exists on the table.
- **Admission decides on the stored switches.** Both switches have no
  environment override, so a missing row is off. The capture transaction
  share-locks the two `runtime_config_entries` rows and refuses unless both
  are stored as true, and the switch-off guard reads the stored admission
  switch rather than one replica's cache. Turning admission off therefore
  waits for every admission already deciding on it, and the unfinished-clone
  count that follows sees each operation it admitted. A replica whose cache
  still says open cannot admit.
- **Refusals name their cause.** A source workload without a live deployment,
  a release-pinned deployment, shared environment secrets, legacy stage queue
  bindings and unscoped work policies answer 409
  `environment_full_clone_unavailable` with the field at fault. They no longer
  answer 404 for an existing environment or a 503 that never clears.
- **A failed or refused restore ends the clone at once** with
  `target_database_failed` (the failed target is no longer read as a
  conflict), rather than retrying to the deadline while restored copies bill.
- **Every host that reaches a clone target's deployment honours the readiness
  gate**, including deployment previews and aliases.
- **An unfinished clone target cannot be protected**: the protection update
  refuses it in the same statement, because publication refuses a protected
  target and compensation would then delete it.
- **A healthy step ends a failure streak**: a release keeps at most the
  lease's attempt count, never below one (zero with no lease still means an
  unleased legacy writer), so normal waits no longer push later failures to
  the five-minute backoff.
- Deleting a published full-clone stage removes the clone's restored
  databases (a project with stages cannot be deleted; #6037). The dedicated
  clone worker configures durable entities as the in-process coordinator does.
