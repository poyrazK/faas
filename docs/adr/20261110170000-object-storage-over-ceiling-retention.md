# ADR-20261110170000: Object storage held above its ceiling is deleted after notice

- **Status:** accepted (owner, 2026-11-10: "Notify, then delete", on the billing launch review; amended the same day for Object Lock, decision 8, owner choice "30-day delete + lock cap"; open questions answered 2026-11-11: tighter lock caps, 30 days on every plan, newest first, a $0 cap starts the clock)
- **Date:** 2026-11-10
- **Owner:** apid and object storage maintainers
- **Amends:** [ADR-20261101214152](20261101214152-object-storage-ceiling-follows-spend-cap.md): data above the ceiling is no longer kept indefinitely. Also bounds Object Lock retention per plan.

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

- **Data above the ceiling is kept forever and never billed.**
  - Under ADR-20261101214152, when an account stores more than its ceiling,
    writes and downloads stop, but nothing is deleted. That happens after a
    subscription ends, a downgrade, or a lowered spend cap.
  - The month-close charge is clamped to the spend cap
    (`objectStorageChargeWithinCap`), and Free storage is never billed.
- **Two cheap ways to keep data for free.**
  - A Scale account can upload 1,000 GiB and then set a $0 cap. About
    800 GiB above the included 200 GiB is then kept at no charge.
  - Any paid account that lets its plan lapse keeps up to its old ceiling on
    Free.
  - Either way, Gregale pays for the storage.
- **The owner chose:** notify the customer, keep the data for a grace
  period, then delete until the account fits its ceiling.

## Decision

1. **When the clock starts.** A subject is *over ceiling* when its stored
   bytes are above its ceiling. Stored bytes count retained versions, as
   admission counts them; the ceiling is the plan ceiling or a support grant,
   tightened by the spend cap. The first sweep that sees it records
   `over_ceiling_since` and schedules deletion 30 days later
   (`api.ObjectStorageOverCeilingGraceDays = 30`).
2. **What stops the clock.** Getting back under the ceiling clears it: by
   deleting objects, raising the cap, upgrading, or a support grant. A later
   breach starts a new 30 days.
3. **Notice.**
   - An email goes out when the clock starts, 7 days before deletion and
     1 day before. Each names the bytes above the ceiling, the date, and the
     ways to stop it.
   - The usage API, `gregale bucket usage` and the dashboard show
     `deletion_scheduled_at`.
4. **What is deleted, in order**, until the subject fits:
   1. noncurrent (retained) versions, oldest first;
   2. incomplete multipart uploads;
   3. current objects, newest first, so data stored before the account went
      over is kept longest.

   Protected versions follow decision 8: the platform releases legal holds
   and bypasses GOVERNANCE retention once the grace has passed. Only
   COMPLIANCE retention, which nobody can shorten, is left until it lapses.
5. **Record.**
   - Every deletion pass writes an audit row with counts and bytes.
   - A final email lists what was deleted.
   - Deleted objects are not recoverable.
6. **Organizations** that own their resources follow the same rules as
   accounts.
8. **Object Lock is bounded by plan, and the platform can remove it**
   (owner, 2026-11-10, object storage audit OS-6).
   - Today any plan, Free included, may set GOVERNANCE or COMPLIANCE retention
     up to 100 years (`api.MaxObjectLockRetentionYears`), on objects or as a
     bucket default. Account erasure (`objectstorage.CleanupOwnedBucketObjects`)
     stops at the first protected version it meets and retries hourly forever,
     so it never completes. The protected data stays stored, unbilled, after
     the account is gone.
   - Object Lock becomes paid-only, in both modes. Retention is capped per
     plan (owner, 2026-11-11): Free none; Hobby 90 days; Pro 1 year; Scale
     7 years. The caps go in `pkg/api/limits.go` as
     `ObjectStoragePlanLimits.MaxLockRetentionDays` (0 = Object Lock not
     included). Legal holds have no duration and stay available on paid
     plans; the platform releases them as below. A retention date,
     default retention or bucket lock configuration beyond the plan's cap is
     refused at write with the limit and the observed value. Existing longer
     retention is reported, not shortened.
   - When deletion is due (this ADR) or account erasure runs (staged
     deletion's grace expiry, [ADR-021](021-account-export-and-staged-deletion.md)), the platform releases legal holds and deletes GOVERNANCE
     versions with `x-amz-bypass-governance-retention` (GCS: release
     temporary/event holds). A protected version no longer stops the pass:
     cleanup deletes everything it may, records the COMPLIANCE versions it must
     keep, and schedules the next attempt at their earliest retain-until
     instead of hourly.
   - Residual: COMPLIANCE versions written within a plan's cap outlive a lapse
     by at most that cap (Scale: 7 years). They stay read-blocked and are
     listed in the final email and the audit row. Accepted, because COMPLIANCE
     exists precisely so that no one, Gregale included, can delete early.
   - A downgrade (customer-initiated) is refused while COMPLIANCE retention in
     the account runs past the target plan's cap, like the storage ceiling
     check (`PlanChangeStorage`); a lapse cannot be refused and falls under
     the residual.
9. **Rollout.**
   - `object_storage_over_ceiling_deletion` starts in `shadow`: it records
     clocks, sends no email and logs what it would delete.
   - Next, `notify` sends the emails without deleting.
   - Last comes `on`. Notify must have run for at least 30 days before `on`,
     so no subject is deleted without having had the full notice.

## Owner answers (2026-11-11)

- **Lock caps:** Free none, Hobby 90 days, Pro 1 year, Scale 7 years.
- **Grace length:** 30 days on every plan.
- **Deletion order:** newest current objects first, as in decision 4.
- **A customer's own $0 cap:** starts the clock like a lapsed plan.

## Consequences

- Storage above the ceiling is bounded at 30 days, plus the rollout notice
  period.
- A customer who lowers the cap or lets a plan lapse by mistake has three
  emails and 30 days to act.
- New pieces:
  - a state table with `over_ceiling_since` and `deletion_scheduled_at` per
    subject (expand-only);
  - an apid sweep worker;
  - three mail templates;
  - a deletion pass through the object-storage provider;
  - the usage fields.
- Rejected:
  - billing storage past a $0 cap, which doesn't cover a lapsed Free account;
  - deleting whole buckets, which is coarser than needed.
