# ADR-20261111140000: Complimentary accounts hold a paid plan without a subscription

- **Status:** accepted (owner, 2026-11-11, on the billing launch review: "Optional end date", "Hard limit at allowance", "Operator CLI + audit")
- **Date:** 2026-11-11
- **Owner:** apid and meterd maintainers
- **Builds on:** [ADR-20261103121407](20261103121407-final-egress-plan-change-time-held-usage.md) (held usage), [ADR-20261105120000](20261105120000-free-disk-reaper-downgrade-caps-spend-cap-ceiling.md) (spend caps), [ADR-20261104204500](20261104204500-egress-overage-election.md) (egress overage election)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

- **Some accounts hold a paid plan with no subscription.** Examples are the
  owner's own account, partners and support goodwill. An operator sets the
  plan, and no Polar subscription pays for it.
- **Once billing is live, they fall into two traps:**
  - With no provider identity, meterd never lists their usage. They run a
    paid plan for free indefinitely, including any overage they elect.
  - With a provider identity but no subscription (for example, a former
    subscriber kept on a paid plan), meterd holds every hour as
    `identity_incomplete`. `FaasMeterdHeldUsageExpiring` then warns about
    them until the lookback drops the usage.
- **Nothing records that the plan is intended.** A paid plan without a
  subscription looks the same whether support gave it on purpose or a
  webhook was lost.

## Decision

1. **A complimentary account is recorded explicitly.** The
   `complimentary_accounts` table holds one row per account: the plan
   (hobby, pro or scale), an audit reason, who granted it and when, and an
   optional `expires_at`. apid is the only writer.
2. **Operators set and end it.** They use `gregalectl accounts complimentary
   --account-id ID --plan pro [--until RFC3339] --reason slug --yes`, which
   calls `POST /v1/admin/ops/accounts/{id}/complimentary` (admin mutation,
   audited as `operator.action.complimentary`). `--plan none` ends it.
   - Setting it refuses an account that already has a subscription.
     Otherwise it records the row, moves the plan, holds the spend cap at $0
     and turns off any egress or managed PostgreSQL overage election.
   - Ending it returns the account to Free with no spend cap and removes the
     row. An account that subscribed meanwhile keeps its plan.
3. **The end date is optional** (owner). With one, an hourly apid sweep
   ends the account when the date passes and audits
   `account.complimentary_expired`. Without one, it lasts until an operator
   ends it.
4. **Hard limit at the plan's allowances** (owner).
   - The $0 spend cap stops compute at the included GB-RAM-hours and holds
     object storage at the included amount.
   - Raising the cap (API and dashboard), electing egress overage and
     electing managed PostgreSQL overage are refused with 409. Turning
     overage off still works.
   - A read failure refuses too, so an unbilled account never runs past its
     allowance.
5. **meterd skips complimentary accounts.** Like Free accounts, they are
   neither pushed to the provider nor held. Their usage is still recorded
   in `usage_minutes`.
6. **Scope:** accounts only. A resource-owning organization's plan follows
   its subscription, as before.

## Consequences

- Before billing goes live, every paid account without a subscription is
  either made complimentary (with an end date where it fits) or moved to
  Free. The launch checklist lists this.
- A complimentary account sees its plan and limits like any paid account.
  Spending past the plan needs a subscription.
- Rejected:
  - a permanent flag only, which lets goodwill plans stay forever by
    accident;
  - overage allowed but unbilled, which is a direct cost with no ceiling
    except the overage ceilings;
  - a Product Control UI, which can come later on top of the same route.
