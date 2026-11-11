# Event driven remediation

All thirteen runtime findings in [the investigation log](event-driven-investigation.md)
have local repairs with targeted verification recorded below. Original evidence
is retained. These changes have not been deployed. Deployment qualification
remains pending as requested.

| Finding | Repair | Verification |
|---|---|---|
| ED001 | Durable NATS consumers, clear inherited expiry during promotion, invalidate deleted consumers | Live JetStream idle, deletion recovery and upgrade/acknowledgement preservation passing |
| ED002 | Bounded Kafka read-ahead and payload-free commit progress | Scheduler regression passing |
| ED003 | Forward deferred and scheduled retries through the NATS pool | Production wrapper regression passing |
| ED004 | Bounded idle pending-entry reclaim and fenced active lease refresh | Scheduler Redis regressions passing |
| ED005 | Stamp authenticated app producer on app-key events | MemStore and PostgreSQL regression passing |
| ED006 | Recognize retained matching acceptances before current schema rules | Single and batch regressions passing in both stores |
| ED007 | Apply company access to event retention samples | MemStore and PostgreSQL regression passing |
| ED008 | Capture schema version on change rows, relay and DTO; owner selection | Both stores and real PostgreSQL hook/relay tests passing |
| ED009 | Durable operator replay marker; resume admitted work without a broker handle or intake filtering | Both retry APIs, restart with broker unavailable, changed intake filter, claim/payload guards and migration safeguards passing |
| ED010 | Reject HTTP control characters in event identity fields | API/event contract regressions passing |
| ED011 | Honor permanent routing errors after matching | Scheduler regression passing in both stores |
| ED012 | Read the live fenced claim before expiring a circuit probe | MemStore and PostgreSQL regression passing |
| ED013 | Remove NATS URL userinfo from errors; scrub retained health during migration | Credential tests passing in both stores |
| EDG001 | Retain the deployed event journey and promotion gate | Journey compiles; deployment, reference OVH run and primary/standby drill intentionally pending |

## Rollout and operational follow-up

1. Apply the new platform migration, upgrade the control plane, then start the
   updated scheduler.
   It adds the operator replay marker/index, recovers already acknowledged
   unified replays still pending, and redacts NATS userinfo in retained health.
   Older schedulers can continue beside the new schema; updated workers
   deliver marked replays with the existing trigger claim generations.
   Index creation runs concurrently; recovery and diagnostic cleanup commit
   bounded batches. Downgrade refuses to discard unsettled replay work.
   After the control plane upgrade, resubmit stranded direct record retries
   made by older versions. They have no unified dead-letter replay stamp for
   the backfill to recognize. The same applies to old-version retries made
   during the rolling upgrade; use the updated record retry API.
   NATS stream consumer limits must permit durable consumers without expiry.
   Existing ephemeral consumers are promoted and then updated to clear their
   inherited inactivity threshold, preserving the acknowledgement position.
2. Reapply the exported Data API hook SQL as an owner migration. The SQL is
   repeatable and preserves recorded changes. Select a version with
   `SELECT gregale_hooks.set_schema_version('api.orders', 'v1')`, register
   compatible schemas for the generated event types, then enable the relay.
   Configuration changes affect future row changes. Definitively rejected
   unversioned rows need an explicit owner-selected version before retrying;
   accepted or uncertain publications must retain their original identity.
3. Review historical NATS token exposure in retained logs and rotate affected
   credentials through the normal credential workflow. The migration cannot
   remove copies already exported to a logging system.
4. Review historical app producer events that lacked producer metadata.
   Immutable acceptance snapshots cannot reliably reconstruct past membership;
   the code repair prevents new events from taking the unrestricted path.
5. Run `TestEventDelivery` against the intended deployed test
   target, then the reference-host V12 benchmark and failover drill required by
   ADR-20261029093000. Unit tests do not establish the reference-host gate.
   Keep the capability in preview and customer pages unpublished until that
   evidence is available.

## Verification before integration

The table below records the repair-phase checks before updating the branch
to current main. The [evidence directory](../ops/evidence/20261111-event-driven-investigation/README.md)
keeps the original failures and separate `fix-*.log` results. The checked-in
verification recipe uses the Makefile's bounded compiler flags.

| Check | Final result |
|---|---|
| Event API, Data API ingress and event contract packages | All three complete package suites pass; MemStore and PostgreSQL regressions run |
| Scheduler | 49 selected tests pass, zero skips; includes live NATS 2.11.17, scripted Kafka and miniredis |
| Trigger and dead-letter API compatibility | 33 tests pass, zero skips |
| Replay safeguards, migration round trip and history stress | 3 top-level tests pass, zero skips; both stores and 20,000 PostgreSQL progress writes |
| Data API hooks and relay | 24 Node tests pass, zero skips; actual PostgreSQL schema/version capture and repeatable upgrade |
| CLI hooks and HTTP/2 bridge | 4 CLI tests and the bridge regression pass |
| SDKs | Pinned Node/Python regeneration changes only the optional field; TypeScript build and Python model round trips pass |
| Migration, schema and SQLC | Expand/contract lint and SQLC drift checks pass; schema snapshot/hash regenerated from an isolated database |
| API contracts | OpenAPI lint, AST parity, embedded spec parity, SDK spec, documentation links and version-skew checks pass |
| Static checks | All 14 affected packages clear after the final appevents/sched recheck; store, seal-domain, problem-detail and trimpath guards pass; Go formatting and diff whitespace checks pass |

The initial broad event-state run exposed a history stress fixture that ran
past its one-minute claim without renewal. The fixture now renews the claim
as a production sweep does; its targeted rerun passes. Claim expiry and stale
token behavior retain their separate tests. The final API compatibility run
also confirms that specific non-replayable errors retain their existing codes.

OpenAPI lint exits successfully but reports 4,158 warnings and 376 informs
across the repository. Migration and version-skew checks compare against
`46aad2a12` because no ancestor release tag is present locally. These results
do not replace deployed qualification, broker failover testing or the reference
host benchmark. The dedicated local NATS test container was stopped afterward.

## Integration with current main

The repair branch was rebased onto `8b12ce5d6`. Main already contains the app
producer company stamp and the broader `TestEventDelivery` journey; both are
retained, with the isolation regression included here. Its newer change-hook
backlog bound, drop reporting, drain and retry behavior are also retained.

Schema version setup is included in `data-api hooks export --upgrade`.
Partition changes use the configured parent relation, and owner retries keep
the captured version when moving failed rows to new queue identifiers.
The updated PostgreSQL hook and relay suite passes 34 tests with zero skips;
see the integration evidence alongside the earlier results.

The migration generator allocated
`20261111160000419_external_trigger_replay.sql` after main's latest migration.
The migration also disables the short statement timeout while building its
index and repairs an invalid index left by an interrupted build; that recovery
was exercised in an isolated PostgreSQL database. The schema snapshot/hash and
SQLC were regenerated from a fresh local database,
and both SDKs were regenerated against the integrated spec. The pull request's
checks are the integration gate. Deployment qualification remains pending.
