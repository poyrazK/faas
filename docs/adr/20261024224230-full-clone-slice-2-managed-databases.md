# ADR-20261024224230: Full environment clones, slice 2 — managed databases at one point in time

Date: 2026-10-24
Status: Accepted
Amends: ADR-590 (complete project-environment clones), ADR-20261024123122 (slice 1)

## Context

Slice 1 opened full clones for sources without managed data. A source whose
workloads bind a managed PostgreSQL database was still refused at admission
(`managed_postgres_bindings: data_checkpoint_unavailable`), and the clone
pipeline refused it in three places:

- **Capture** returned `data_checkpoint_unavailable` for any database, because
  ADR-590 waits for a coordinated writer checkpoint across databases, buckets
  and configuration. That checkpoint's drivers exist only in tests.
- **Publication** refused any workload with a database binding: a value proof
  cannot show the database was copied.
- **Compensation** had no path that removes a restored database.

The copy itself already works. With a recorded capture point, the coordinator
reserves a clone-owned target per source database. It restores the database
through the provider's point-in-time restore (Neon branches at a timestamp)
and prepares fresh target bindings. The managed-PostgreSQL service records a
restore proof when the provider reports the target ready with its lineage:
source resource and point.

## Decision

1. **Capture freezes one data point, with no writer barrier.** After the
   configuration capture has committed, the capture worker reads the
   control-plane clock under its lease. It then validates the live source
   against the frozen capture in a snapshot taken after that instant. Every
   bound database is restored at that point.
   - **If the source changed in between** (values, secrets, settings,
     bindings, artifacts or workloads), the clone fails with
     `source_changed_during_capture` and compensates. That rules out pairing
     the captured workloads with data from another revision.
   - **Each copy is crash-consistent at the point**, exactly what a customer
     point-in-time restore gives. Several databases are restored at the same
     instant.
   - **The point is recorded once.** A retry reuses it and never picks a new
     one.

   ADR-590's coordinated checkpoint remains the plan for clones that also copy
   buckets, where an object version manifest must line up with the database
   point.

2. **Copying waits for the provider to pass the point.** Capture records the
   point and ends the pass. A later pass restores only once the point is 30
   seconds old (`cloneDataPointSettle`), so neither clock skew nor
   write-ahead-log ingestion lag can put the requested point ahead of the
   provider's history.

3. **Admission accepts point-in-time-restorable databases.**
   `managed_postgres_bindings` no longer blocks. A bound database without a
   restore window is named `point_in_time_restore_unavailable`. Buckets still
   block with `data_checkpoint_unavailable`.

4. **Publication requires a restore proof per database.** For every database
   a workload binds:
   - the clone's resource for that source is ready;
   - its target is a ready clone-owned database with a provider restore proof
     from that source at the recorded point (the same checks the
     managed-PostgreSQL service applies before it adopts a restore);
   - the workload's prepared target binding uses that target.

   The memory store cannot read proofs and still publishes no database.

5. **Failure and compensation remove the copy.** A clone is directly
   compensable when it holds no data checkpoint, object copy or unmanaged
   PostgreSQL resource. Such a clone fails when:
   - a target deployment ends (`target_deployment_failed`);
   - a target database restore fails (`target_database_failed`);
   - the source changes before the data point (`source_changed_during_capture`);
   - it misses its deadline (`clone_deadline_exceeded`): 30 minutes without
     databases, 2 hours with them.

   Compensation then:
   1. cancels the target deployments and waits for them to drain;
   2. withdraws the target bindings the clone prepared, soft-deleting them
      with their stage secrets;
   3. deletes each restored database through
      `managedpostgres.Service.DeleteEnvironmentCloneTarget`, an internal path,
      because customers cannot see an unpublished clone database;
   4. once no restored database remains, removes the target environment and
      marks the operation compensated.

   The withdrawn credentials are roles inside the deleted databases.

## Consequences

- A project whose HTTP workloads bind managed PostgreSQL databases with a
  restore window can be cloned in one command. The stage gets its own copy of
  each database as of the clone's data point, with fresh credentials, and
  stage writes never reach production.
- The stage's data reflects an instant: transactions still in flight at that
  instant are absent, as after any point-in-time restore. A database restored
  at one instant has no cross-database transactional guarantee beyond that.
- Each copy is a managed database billed and counted like any other; the
  restore reservation applies the plan's database limit.
- No live Neon run is part of this change: the adapter's restore, lineage and
  deletion paths are those its existing qualification covers, and these tests
  drive the service with a fake provider.

## Rejected alternatives

- **A writer barrier before the point.** It would pause production writes to
  gain nothing a single point-in-time restore lacks while only databases are
  copied; it remains necessary once buckets join the clone.
- **Choosing the point inside the configuration capture transaction.** That
  point precedes the commit, so a source edit racing the commit would not be
  bracketed by the later validation.
- **Deleting clone databases through the customer delete path.** Unpublished
  clone databases are invisible to customers by design; widening customer
  visibility to reach them would expose half-built stages.
