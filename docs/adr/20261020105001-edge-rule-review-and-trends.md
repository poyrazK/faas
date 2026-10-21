# ADR-20261020105001: Edge-rule review and per-rule trends

- **Status:** accepted
- **Date:** 2026-10-15
- **Related:** ADR-960 (log mode and hit counts), ADR-964 (security events),
  ADR-986 (protection recipes), ADR-20261020105000 (suggested protections)

## Context

Protection recipes and suggestions add rules in log mode (ADR-960), where
they are matched and counted but never act. Nothing then tells the user when
a rule has seen enough traffic to decide, and a rule that matches nothing
for weeks sits in the table unnoticed. The hourly hit buckets (14-day
retention) already hold everything needed to answer both questions; the
page only showed a 24-hour total.

## Decision

1. **Hourly series.** The hit store gains a read of an app's hourly buckets
   per rule and outcome. `GET /v1/apps/{slug}/edge-rules/stats` takes
   `series=true` and then returns, per rule, its matched-plus-logged total
   for every hour from `since` through the current hour. No new counter,
   table or write path.

2. **Review rules.** A pure function in `pkg/api` (`ReviewEdgeRule`)
   classifies each enabled, unexpired rule:
   - **collecting:** in log mode and changed less than 24 hours ago;
   - **ready:** in log mode and unchanged for at least 24 hours, with its
     matches counted from the hour of its last change (an edit restarts the
     evidence) and an average per day;
   - **quiet:** unchanged for at least 7 days and no matches in the last 7
     days, in either mode.
   Enforced rules with matches and disabled rules are not reported. The
   rule's `updated_at` (kept by the `edge_rules_set_updated_at` trigger) is
   the last change.

3. **Dashboard.** The edge-rules page shows a "Rule review" card above the
   rules table: ready rules first (most matches first) with a one-click
   **Start enforcing** (the existing mode switch) and a link to the rule's
   sampled log-mode requests, then collecting rules, then quiet ones. The
   rules table shows a 7-day sparkline per rule (28 six-hour totals) next
   to the 24-hour count; both come from one read of the hourly buckets.

4. **CLI.** `gregale edge-rules review` prints the same review with the
   command for each rule (`update --mode enforce`, `events`, `rm`), and
   `gregale edge-rules stats` gains a TREND column (hourly for 24h, six-hour
   for 7d).

## Consequences

- Moving a rule from log mode to enforcement is one decision on evidence the
  page already shows, instead of reading counters and events separately.
- The review says what a rule matched, not what its action would have done:
  for a throttle rule, matches are the requests it applies to, not those it
  would have rejected.
- The 24-hour minimum and 7-day quiet window are fixed constants; tuning
  them is a change to one pure function and its table test.
