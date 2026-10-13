# ADR-1017: Recovery policy change preview

## Decision

Add a read-only POST to `/v1/apps/{slug}/recovery-policy/change-preview` using the existing policy update request. Require app read access and MFA, bounded JSON, valid policy settings and the observed configuration version, including zero for an unconfigured app. A stale configuration returns conflict. The CLI accepts `recovery-policy --app APP --file POLICY.json --preview`; it does not refresh the file's expected version or submit an update.

Use one Postgres repeatable-read read-only transaction or the memory mutex for the snapshot. Reuse queue observations and existing preparation/inspection logic. Include all pending automatic intents that replacement suppresses, unresolved applied intents from any configuration version that retain serial slots, existing hourly charges, proposed execution/preparation budget headroom, and cooldowns recalculated from retained timestamps. Queue effects are bounded to the existing history-page limit, with total counts and an explicit truncation flag. Replacement clears automatic pause and failure streak, while charges, committed children and retained preparation targets survive.

Inspect the five newest retained workflow recovery jobs matching the proposed workflow names, independently of the scheduler's UUID cursor. Reuse the existing jobs-per-poll limit and return at most the existing batch run limit of candidate decisions. Explicitly report job and candidate truncation. Simulate preparation with retained target deduplication, generation limits, preparation budget and cooldown; eligible disabled-policy targets report `policy_disabled`, while simulated preparation reports `would_prepare`. Stage simulated budget/target observations only in local memory. No reviews, targets, plans, executions, charges, notifications, evaluations, scheduling timestamps or controls are written.

## Consequences

Operators see the consequences of full replacement even when filters are unchanged: old pending retries are suppressed, and retained preparation targets can prevent those generations from being prepared again. Automatic retry mode does not convert existing preview-mode plans. A proposed smaller budget does not refund spent charges or finalize a child.

The response is advisory and contains no executable plan or update authorization. Applying the same file remains a separate deploy-write/MFA operation with existing configuration-version CAS. Concurrent controls, failure pauses, queue progress or evidence changes may occur without changing that version; the preview does not freeze them or bypass execution-time checks. Execution quota and admission are not guaranteed by simulated preparation. The sample covers retained recent matching jobs, not a full history scan or the scheduler's exact future processing order. No migration or new polling loop is required.
