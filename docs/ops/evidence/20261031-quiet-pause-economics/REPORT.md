# Unbilled quiet pause economics re-run, 2026-10-31

The pre-acceptance check ADR-20261023171905 "Economics" asks for: re-run
`freeze_sim.py` with a policy G (quiet pause after 2 s, unbilled; freeze at
the idle timeout) on ADR-1430's plan table, $0.01 overage and the 30 s
function idle timeout, then reconcile with the financial model.

## Result

Turning quiet pause's paused seconds unbilled costs about **0.2% of revenue**
for function apps and at most about 0.4% if every app opted into
`between_requests`. Compute COGS does not change: a quiet-paused VM still
holds its RAM. The visible effect is on customers' usage: billed GB-h per
app-month falls by 60–72%, and far fewer Free apps reach Free's hard stop.

Per app-month, ADR-1430 plans (USD), production's 10 min `FrozenTTL`:

| | E: pause billed (today) | G: pause unbilled | change |
|---|---|---|---|
| **Function apps** (30 s idle timeout; the realistic case) | | | |
| billed GB-h | 46.1 | 18.2 | −60% |
| revenue | $3.972 | $3.963 | −$0.009 (−0.23%) |
| Hobby overage, per Hobby app | $0.115 | $0.027 | |
| Free apps over Free's 20 GB-h (hard stop) | 33.7% | 14.1% | |
| Hobby apps over their 200 GB-h | 11.9% | 3.2% | |
| resident GB-h (RAM held) / compute COGS at 0.003 per GB-h | 46.1 / $0.138 | 46.1 / $0.138 | none |
| **Every app `between_requests`** (plan idle timeouts; upper bound) | | | |
| billed GB-h | 64.8 | 18.2 | −72% |
| revenue | $3.980 | $3.963 | −$0.017 (−0.43%) |
| Free apps over 20 GB-h (hard stop) | 39.4% | 14.1% | |
| Hobby apps over 200 GB-h | 18.0% | 3.2% | |
| resident GB-h / compute COGS | 64.8 / $0.194 | 64.8 / $0.194 | none |

Pro and Scale apps never exceed their included GB-h (1,000 and 3,000) in
either policy, so all overage revenue comes from Hobby. Revenue per
app-month averages over the 80/11/8/1 Free/Hobby/Pro/Scale mix (four free
apps per paying one; spec V8).

Wakes, latency and density are the same for E and G: billing does not change
what the VM does. With ADR-1430's doubled RAM per app, RAM binds before vCPU
on an EX44 (706 function apps; 627 when every app opts in), as it does for E.

## Correction to earlier tables

`freeze_sim.py` counted Free usage past its included GB-h as overage revenue.
Free is never billed overage; meterd hard-stops it (spec §4.7,
`pkg/meter/pusher.go`, `pkg/billing/stripe/products.go`). The ADR-906,
ADR-1151 and ADR-20261023171905 tables therefore overstate revenue, and
overstate what unbilled quiet pause costs: on the old euro table, about two
thirds of G's "−€0.11 per app-month" (€0.08) was Free overage that is never
collected.
`FREE_OVERAGE=1` reproduces those tables exactly (`eur-allapps-repro.json`:
E 29.4 GB-h / €3.943, G 9.3 GB-h / €3.829), so the simulation itself is
unchanged.

## Reconciliation with the financial model

`ex44_faas_financial_model.xlsx` is not in the repository or on the machine
this ran on, so the reconciliation is against the October 2026 COGS report's
figures recorded in ADR-1430: compute at about €0.003 per GB-RAM-hour against
$0.01 overage, and about 67% gross margin at 500 paying customers. G leaves
COGS unchanged and lowers revenue by 0.23–0.43%, which moves a 67% gross
margin to 66.9%. **The owner still has to check the spreadsheet** (whether it
counts overage revenue, and from which plans) before the billing switch
leaves shadow.

## Method

```sh
python3 -m venv venv && venv/bin/pip install numpy
# apps.npz: 3,000 HTTP-triggered apps, days 1-7 of the Azure Functions 2019
# trace (load_trace.py, as in the README)
FROZEN_OWN_FRAC=0.15 PLAN_TABLE=adr1430 FUNCTIONS=1 venv/bin/python -P -E \
  scripts/experiments/frozen-tier-economics/freeze_sim.py apps.npz usd-functions.json
FROZEN_OWN_FRAC=0.15 PLAN_TABLE=adr1430 venv/bin/python -P -E \
  scripts/experiments/frozen-tier-economics/freeze_sim.py apps.npz usd-allapps.json
FROZEN_OWN_FRAC=0.15 PLAN_TABLE=eur-2026-10-12 FREE_OVERAGE=1 venv/bin/python -P -E \
  scripts/experiments/frozen-tier-economics/freeze_sim.py apps.npz eur-allapps-repro.json
```

The three result files are in this directory.

## Caveats

- The traffic is the public Azure Functions 2019 trace, not Gregale's.
  vmmd's observe mode and the ledger's shadow week measure the same
  quantities on production traffic and should replace these numbers.
- One app per account, and the plan mix is the financial model's assumption.
- The model bills exactly 2 s per quiet period. Production pauses 2.0–2.25 s
  after the last request (the pause pass ticks every 250 ms), and meterd
  floors paused seconds per instance-minute, so G bills a little more than
  modeled and the revenue change is slightly smaller.
- A Free app that hits the hard stop keeps serving in the model; it only
  counts how many would be stopped.
- Dollars and euros are treated as equal, as the COGS report does
  (ADR-1430).
