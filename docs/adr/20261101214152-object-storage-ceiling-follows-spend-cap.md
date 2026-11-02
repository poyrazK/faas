# ADR-20261101214152: Paid object storage stops where the spend cap stops paying

- **Status:** proposed
- **Date:** 2026-11-01
- **Amends:** ADR-1320 (object-storage plan allowances, ceilings and the
  overage cap). It closes ADR-1320's open "runtime cap gate for object
  storage".
- **Decision:**
  1. **A paid account's storage ceiling follows its spend cap.** The ceiling
     is the plan's included storage plus the GiB-months the cap buys at the
     published price (`api.ObjectStorageMillicentsPerGiBMonth`, $0.03 per
     GiB-month, ADR-1430). With the default caps that is:

     | Plan | Included | Default cap | Ceiling |
     |---|---|---|---|
     | Hobby | 10 GiB | $2 | 76 GiB |
     | Pro | 50 GiB | $10 | 383 GiB |
     | Scale | 200 GiB | $30 | 1,200 GiB |

     A customer who raises the cap raises the ceiling at once. A cleared cap
     ("no cap") leaves the operator's ceiling. Free keeps its 1 GiB. The
     operator's ceiling still applies when it is lower. A write past the
     ceiling answers `402` with kind `account_bytes`, as Free's ceiling
     already does, and the usage endpoint reports the ceiling as
     `max_account_bytes`.
  2. **Downloads stop while an account stores more than its ceiling.** An
     object body is refused with `402 account_bytes`. Listing, `HEAD` and
     `DELETE` keep working, so the customer can find and delete what takes
     the account back under. Writes already stop there. Stored bytes only
     exceed the ceiling without a write being admitted when the ceiling
     moves down under them: a subscription that ends moves the account to
     Free, or the customer lowers the cap.
  3. **A plan downgrade is refused while the account stores more than the
     target plan's ceiling,** computed with the cap the account would hold
     after the change (ADR-20261029180000). The refusal names how much to
     delete or that the cap can be raised, as it does for apps and RAM.
- **Why:** ADR-1320 keeps storage serving at the cap "within the plan
  ceilings", unbilled past it. Only Free had a plan ceiling, so every paid
  plan's ceiling was the operator's, which the operator notes size for the
  largest plan: a Hobby account could keep a Scale-sized store and pay at
  most its $2 cap. A subscription that ended moved the account to Free with
  all its bytes, which stayed readable and unbilled.

## Consequences

- Storage alone never bills past the cap. Compute shares the cap, and month
  close bills storage last (ADR-1320), so in a month where compute takes
  part of the cap the unbilled storage is at most that part's worth.
- The ceiling is read with the plan in the usage snapshot that every
  admission already reads; the gateway makes no extra database call.
- No data is deleted. How long bytes above the ceiling are kept, and the
  notices before any deletion, are not decided here.
- No production account is affected today: the operator ceiling is 16 MiB.

## Alternatives considered

- *Paid plan ceilings as their own numbers in the financial model.* That
  needs new business values; the cap is already the customer's own spend
  bound and moves with it.
- *Park object storage at the cap* (rejected in ADR-1320): it needs a cap
  read on every gateway request. The ceiling here comes from the snapshot
  admission already reads.
- *Refuse every request above the ceiling,* as at the egress ceiling. The
  customer could not list the bucket to find what to delete.
- *Delete bytes above the ceiling after a grace period.* Irreversible and
  needs customer notices; left for a later decision.
