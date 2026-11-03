# ADR-20261101113742: Egress coverage from cumulative counters, usage pages show the bill, floors follow the billing subject

- **Status:** accepted
- **Date:** 2026-11-01
- **Owner:** billing maintainers
- **Amends:** ADR-1748 (what proves egress evidence complete), ADR-060 and ADR-071 (whose plan decides a floor), ADR-048 §5 (the daily rollup's window)
- **Related:** ADR-1162, ADR-20261030093512, ADR-20261031100000, ADR-20261031161842, #6029 (organization billing subjects)

## Context

A review of meterd's metering on main dcb51d69a7 (2026-11-01) found five gaps.

1. **No minute was ever egress-complete.** meterd recorded every rolled
   minute compute-complete and egress-incomplete, and nothing else wrote
   `financial_sampling_windows.egress_complete`. Strict account egress
   (ADR-1748) needs complete egress evidence to activate, so it could never
   be created; every budget covering egress reported incomplete coverage
   and never projected, and the cost report listed egress as
   `missing_sampling_windows` for good.
2. **The usage pages did not show the bill.** `GET /v1/usage/summary`, the
   dashboard's usage page and the operator tenant view computed overage as
   usage above today's plan allowance, in floats, at a hard-coded rate. The
   bill, the spend cap and the billing page follow the month's plan history
   hour by hour (ADR-20261030093512, ADR-20261031100000). After a plan change
   the two disagreed, and a past month was shown against today's plan. The
   per-app usage endpoint compared one app's usage over any window with the
   whole account's monthly allowance and called the difference overage.
3. **A company app's floor followed its creator's plan.** schedd's floor
   trigger and meterd's floor cap read the plan of `apps.account_id`, while
   meterd billed the floor to the app's billing subject, its organization
   when the organization owns its resources. A paid organization of a Free
   member never got its floor; a Free organization of a paid member was held
   one and billed for it to a Free subject, which the pusher never delivers.
4. **usage_daily missed late rows.** The rollup recomputed yesterday and
   today, but meterd writes a missed minute up to `api.MeterUsageFinalAfter`
   (72 h) late. After a meterd outage of more than a day the daily usage
   pages, `/v1/usage/daily` and daily cost alerts stayed short of the bill.
5. **A counter reset dropped egress.** A host-interface counter below its
   checkpoint, after a vmmd or interface restart, was recorded as zero. The
   restarted counter counts from zero, so that first reading was new egress.

## Decision

1. **A fully rolled minute is egress-complete when the network source is
   cumulative.** meterd's production source reports vmmd's cumulative
   per-instance counters, which the Store checkpoints exactly once in the
   transaction that appends the delta (`AppendNetworkUsageObservation`). A
   missed, failed or caught-up tick then loses no bytes: the next reading
   carries them into the minute it is taken. So when the sampler's source
   implements `meter.CumulativeNetworkSource`, meterd records each minute it
   records compute-complete as egress-complete too. Egress is attributed to
   the minute it is observed, which is also how it is billed. A source
   without cumulative counters records compute only, as before.
2. **The usage pages show the billed figures.** `billing.ComputeOverageForMonth`
   returns a month's allowance and billed compute overage from its plan
   history: for the month in progress the figure the spend cap reads
   (`state.CurrentMonthBilledComputeMBSeconds`, also behind
   `CurrentMonthOverageCents`), for an ended month billed through its end.
   `/v1/usage/summary`, the dashboard usage page and the operator tenant view
   use it, in integer cents. The per-app endpoint's `overage_gb_hours` is 0:
   the allowance is the account's, shared by every app. The field stays for
   older clients.
3. **A floor follows its app's billing subject.** schedd's floor trigger
   resolves the plan and status of `state.AppResourceAccount` (the optional
   `floor.AppPlanResolver`, which schedd's resolver implements), and meterd's
   floor cap reads the same subject's plan. A paid organization's apps get
   their floors whatever their creators' plans; a Free organization's apps
   get none.
4. **The daily rollup also recomputes the late days.** On its first tick and
   then hourly it recomputes every complete UTC day back to
   `api.MeterUsageFinalAfter`, besides yesterday and today on every tick.
5. **A counter below its checkpoint is a reset.** Its whole value is new
   usage, as for a missing checkpoint; the baseline moves to it.

## Consequences

- Strict account egress can activate, and egress budgets project, once a
  month's minutes are recorded egress-complete: from the first month that
  starts after this release, or for accounts created after it.
- Bytes still lost: those an instance sends between its last reading and
  its teardown, and those between vmmd's last reading and a restart. Both
  are bounded by the stats poll and were lost before this change.
- `/v1/usage/summary` `included_gb_hours` is the month's time-weighted
  allowance and can be fractional before flooring; after a plan change the
  usage page and the billing page now agree.
- Clients that show the per-app overage chip stop showing it.
- An organization whose apps had floors through a paid creator, while the
  organization itself is Free, loses those floors at the next floor tick.
  It was never billed for them.
- The rollup runs up to three more day scans once an hour.
- After a vmmd restart, each instance's next reading is counted in full. A
  stale reading after a newer one would be counted twice; schedd's poller
  serves one vmmd's readings in order, so a reading only goes back on a
  restart.

## Rejected alternatives

- **Egress coverage from node heartbeats.** A fresh heartbeat proves the
  node reported, not that every instance's bytes were read; cumulative
  counters make the reading time irrelevant to totals.
- **Removing `overage_gb_hours` from the per-app response.** Breaks
  release N-1 clients (docs/release-compatibility.md).
- **Rolling every retained day on every tick.** Up to 13 months of scans
  every 5 minutes for rows that can only land in the last three days.
