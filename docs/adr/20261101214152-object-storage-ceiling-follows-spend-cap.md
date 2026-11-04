# ADR-20261101214152: Paid object storage stops at a plan ceiling that support can raise

- **Status:** accepted (owner, 2026-11-02)
- **Date:** 2026-11-01, revised 2026-11-02
- **Amends:** ADR-1320 (object-storage plan allowances, ceilings and the
  overage cap). It closes ADR-1320's open "runtime cap gate for object
  storage".
- **Decision:**
  1. **Each paid plan has its own storage ceiling, and support can raise
     one account's.**

     | Plan | Included | Ceiling | Most overage a month |
     |---|---|---|---|
     | Free | 1 GiB | 1 GiB | none |
     | Hobby | 10 GiB | 20 GiB (2×) | $0.30 |
     | Pro | 50 GiB | 250 GiB (5×) | $6 |
     | Scale | 200 GiB | 1,000 GiB (5×) | $24 |

     - A spend cap below what that storage costs lowers the ceiling to the
       included storage plus the GiB-months the cap buys at
       `api.ObjectStorageMillicentsPerGiBMonth` ($0.03 per GiB-month,
       ADR-1430). A $0 cap stops at the included storage. The default caps
       pay for more than the plan ceilings, so they don't bind.
     - Support grants a paid account, or a resource-owning org, another
       ceiling (`object_storage_ceiling_grants`, written only by apid through
       `POST /v1/admin/ops/accounts/{id}/object-storage-ceiling`, which is
       `gregalectl accounts object-storage-ceiling`). The grant replaces the
       plan's ceiling; the spend cap still tightens it. A Free subject ignores
       a grant: its storage is never billed.
     - The operator's `max_account_bytes` still applies when it is lower.
     - A write past the ceiling is refused with kind `account_bytes`, and the
       usage endpoint reports the ceiling as `max_account_bytes`.
  2. **Downloads stop while an account stores more than its ceiling.** An
     object body is refused with `402 account_bytes`. Listing, `HEAD` and
     `DELETE` keep working, so the customer can find and delete what takes
     the account back under. Writes already stop there. Stored bytes only
     exceed the ceiling without a write being admitted when the ceiling
     moves down under them: a subscription that ends moves the account to
     Free, or the customer lowers the cap.
  3. **A plan downgrade is refused while the account stores more than the
     target plan's ceiling,** or the grant, computed with the cap the account
     would hold after the change (ADR-20261029180000). The refusal names how
     much to delete, and whether raising the cap or asking support helps, as
     it does for apps and RAM.
  4. **Data above a ceiling is kept.** Nothing is deleted for the customer,
     however long it stays above (owner, 2026-11-02). Downloads stay off
     until it is back under.
- **Why:** ADR-1320 keeps storage serving at the cap "within the plan
  ceilings", unbilled past it, but only Free had a plan ceiling. Paid plans
  fell back on the operator's ceiling, sized for the largest plan. A first
  revision derived each paid ceiling from the spend cap, which gave Hobby 76
  GiB and Pro 383 GiB at the default caps and grew with any cap a customer
  raised for compute. The owner judged that too much room for one
  misconfigured client to cost a small customer: small plans get small
  ceilings, and a customer who needs more asks support.

## Consequences

- Storage alone never bills past the cap. Compute shares the cap, and month
  close bills storage last (ADR-1320), so in a month where compute takes
  part of the cap the unbilled storage is at most that part's worth.
- The ceiling is read with the plan in the usage snapshot that every
  admission already reads; the gateway makes no extra database call.
- No data is deleted. We carry the provider cost of bytes above a ceiling,
  which only exist after a downgrade, an ended subscription or a lowered
  cap.
- Raising a ceiling is a support action with an audit row
  (`operator.action.object_storage_ceiling`), not a customer setting.
- No production account is affected today: the operator ceiling is 16 MiB.

## Alternatives considered

- *Derive the ceiling from the spend cap alone* (this ADR's first
  revision). The ceiling would grow with every cap raised for compute;
  rejected by the owner for small plans.
- *A customer setting for the ceiling.* A wrong setup could still store a
  lot; a support grant keeps a person in the loop.
- *Park object storage at the cap* (rejected in ADR-1320): it needs a cap
  read on every gateway request. The ceiling here comes from the snapshot
  admission already reads.
- *Refuse every request above the ceiling,* as at the egress ceiling. The
  customer could not list the bucket to find what to delete.
- *Delete bytes above the ceiling after a grace period.* Irreversible and
  needs customer notices; the owner chose to keep the data.
