# ADR-647: Independent event routing by default

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Default to the independent recipient ownership model from
  ADR-606 for eligible snapshot-backed events. Both the scheduler constructor
  and schedd's environment wiring enable adoption by default. An unset or blank
  `FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED`, or the explicit value `1`, enables it;
  other nonempty values pause adoption. Operators use `0` during mixed-version
  upgrades and when falling back to whole-event routing for new receipts.
- **Why:** A published application event can have successful, retrying, and
  failed consumers simultaneously. Independent ownership allows an operator to
  recover the failed consumer immediately without waiting for a sibling's
  routing backoff or repeating deliveries to successful consumers.

## Routing and compatibility

This changes adoption defaults, not storage or delivery identity. Each eligible
captured recipient receives its own lease, backoff, and replay generation.
Atomic admission (ADR-613) commits the invocation and routing checkpoint together.
Successful consumers keep their original deterministic delivery identity across
recovery, duplicate publication, and subscription deletion. Capacity deferrals
do not consume the routing failure budget (ADR-614).

Memory-store admission and invocation listing resolve both compact and canonical
app UUIDs, matching the UUID identity used by subscriptions and PostgreSQL.

Receipts without captured snapshots retain legacy routing. ADR-648 extends
recipient ownership to captured workflows and mixed workflow/application
receipts using atomic workflow recipient admission. Stores without that
capability retain whole-event routing for workflow receipts. Empty snapshots
settle, and data filters use the immutable captured subscription or trigger.

The flag controls adoption of original acceptance snapshots. Subscription
backfills (ADR-639) already use recipient ownership: their materialization and
delivery continue under either flag setting. Keep their writers compatible,
and start backfill jobs only after the mixed-version upgrade is complete.

Publish acceptance remains durable intent, routing settlement remains distinct
from handler completion, handler execution remains at least once, and there is
no FIFO guarantee. Retention, replay, and dead-letter contracts are unchanged.

## Upgrade and fallback

1. Before upgrading an existing fleet, explicitly set
   `FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED=0` on every scheduler. Keep it set while
   any API or scheduler writer lacks recipient ownership or atomic admission.
2. Apply the additive event migrations and upgrade all writers. Existing
   receipts are adopted only when a compatible scheduler claims them; a
   migration alone does not adopt them. Qualify publish, routing recovery, and
   handler/DLQ recovery in staging for the fleet's supported delivery paths.
3. Remove the override or set `1` to adopt eligible receipts. Fresh installations
   with the matching schema and binaries use the new default directly.
4. Set `0` to pause further adoption if needed. Compatible schedulers continue
   draining adopted recipients and their replays. Already adopted receipts
   are not converted back to whole-event ownership.

Disabling adoption is a scheduling fallback, not permission to downgrade writers.
Retained adopted receipts remain replayable for thirty days after settlement;
old APIs do not understand their generations. Keep compatible binaries while
any adopted receipt remains. The original ownership down migration refuses to
drop the schema while such receipts exist. This change does not deploy a fleet.

## Qualification

Automated scheduler acceptance covers both memory and real PostgreSQL stores:

- Constructor-default adoption of application recipients, empty snapshots,
  captured data filters, and explicit whole-event fallback for new receipts.
- Successful, backoff-delayed, and terminally failed consumers on one event;
  selective replay while a sibling is pending; deletion of the accepted
  subscription; restart with adoption disabled; uncertain admission commit;
  duplicate publication without repeating successful deliveries.
- Capacity isolation between consumers, durable deferrals beyond the failure
  budget, recovery after releasing capacity, and draining after disabling adoption.
- Workflow-only and mixed workflow/application receipts under both ownership
  settings, with one workflow run and one delivery per application consumer.
  ADR-648 records independent workflow recovery qualification.

Existing state acceptance covers concurrent claims and operators, generation and
lease fencing, atomic admission rollback, active-receipt retention, and fair
claiming. Configuration tests cover unset, blank, enabled, disabled, and invalid
flag values, and the generated daemon environment contract records the default.

Additional PostgreSQL integration acceptance drives public publish, receipts,
attempt history, and selective replay through the real fanout/drain and
production HTTP synth server/client. Three HTTP consumers exercise success,
transient retry, and terminal failure or retry exhaustion. A lost settlement
followed by scheduler reconstruction records an unknown attempt, rejects stale
completion, and repeats delivery safely in the test consumer. Selective recovery
preserves successful siblings, plain replay lineage, in-place DLQ generations,
and duplicate publication/replay identity, including after adoption is disabled.

The consumer and instance runtime are test fixtures. Native guest execution,
fleet staging acceptance, and repository-wide CI remain separate release gates.
