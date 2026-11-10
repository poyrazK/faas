# ADR-20261110170000: Object storage held above its ceiling is deleted after notice

- **Status:** proposed (owner, 2026-11-10: "Notify, then delete", on the billing launch review; the details below await review before any code deletes customer data)
- **Date:** 2026-11-10
- **Owner:** apid and object storage maintainers
- **Amends:** [ADR-20261101214152](20261101214152-object-storage-ceiling-follows-spend-cap.md): data above the ceiling is no longer kept indefinitely.

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

   Versions under Object Lock retention or a legal hold are never deleted. A
   subject that stays over only because of protected versions stays read
   blocked, as today.
5. **Record.**
   - Every deletion pass writes an audit row with counts and bytes.
   - A final email lists what was deleted.
   - Deleted objects are not recoverable.
6. **Organizations** that own their resources follow the same rules as
   accounts.
7. **Rollout.**
   - `object_storage_over_ceiling_deletion` starts in `shadow`: it records
     clocks, sends no email and logs what it would delete.
   - Next, `notify` sends the emails without deleting.
   - Last comes `on`. Notify must have run for at least 30 days before `on`,
     so no subject is deleted without having had the full notice.

## Open questions for the owner

- **Grace length.** 30 days, or shorter for Free (for example, 14)?
- **Deletion order.** Is newest-first right for current objects, or should
  it be oldest-first?
- **A customer's own $0 cap.** Should it start the clock like a lapsed plan?
  It is the main way to keep data for free, so the default is yes.

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
