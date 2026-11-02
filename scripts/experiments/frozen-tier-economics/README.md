# Frozen-tier economics simulation

Replays real serverless traffic through today's idle-timeout parking and
through several freeze policies, and reports billed GB-h, revenue, apps per
node, infra cost per app and wake latency. Results and caveats are in
[ADR-906](../../../docs/adr/906-frozen-instances.md#economics-simulated-2026-10-12).

The traffic is the public
[Azure Functions 2019 trace](https://github.com/Azure/AzurePublicDataset/blob/master/AzureFunctionsDataset2019.md):
per-minute invocation counts for 14 days, with memory and duration
percentiles. Production traffic is internal testing, so it cannot stand in
for customers.

```sh
mkdir -p /tmp/azuretrace && cd /tmp/azuretrace
curl -LO https://github.com/Azure/AzurePublicDataset/releases/download/dataset-functions-2019/azurefunctions_dataset2019_azurefunctions-dataset2019.tar.xz
tar xf azurefunctions_dataset2019_azurefunctions-dataset2019.tar.xz
cd -

# 3,000 HTTP-triggered apps, days 1-7 (numpy required)
python3 -P -E scripts/experiments/frozen-tier-economics/load_trace.py /tmp/azuretrace /tmp/apps.npz 3000 7

# FROZEN_OWN_FRAC: a frozen VM's own RAM as a share of its running RAM
FROZEN_OWN_FRAC=0.15 python3 -P -E scripts/experiments/frozen-tier-economics/freeze_sim.py /tmp/apps.npz /tmp/results.json
python3 -P -E scripts/experiments/frozen-tier-economics/show.py /tmp/results.json
```

`FUNCTIONS=1` treats every app as a function app, which idles out after
30 s on every plan (ADR-974) instead of the plan's idle timeout.

`PLAN_TABLE` picks the plans, prices and included GB-h: `adr1430` (the
default) is today's `pkg/api/limits.go` table in US dollars (ADR-1430);
`eur-2026-10-12` is the euro table ADR-906, ADR-1151 and
ADR-20261023171905 were first simulated with, so their numbers reproduce.
Policies `E` and `G` compare quiet pause billed and unbilled
(ADR-20261023171905), with production's 10 min `FrozenTTL` as well as the
1 h one. Each policy also reports resident GB-h (RAM held running or
quiet-paused, which compute COGS follows whether billed or not) at the COGS
report's 0.003 per GB-h, overage by plan and the share of apps over their
included GB-h.

Free usage past its included GB-h is never billed: meterd hard-stops the
account (spec §4.7), so a Free app over its included GB-h is one that would
be stopped, and its overage is not revenue. `FREE_OVERAGE=1` counts it as
revenue, as the ADR-906, ADR-1151 and ADR-20261023171905 tables did, to
reproduce them. The 2026-10-31 re-run is in
`docs/ops/evidence/20261031-quiet-pause-economics/`.

`occupancy.py apps.npz` prints the active vCPU, booked vCPU, resident and
frozen VM counts per instant under ADR-1151's policy (quiet pause plus
freeze at the idle timeout).

The simulation runs in about 10 s and peaks at about 1.5 GB of RAM. Plans, prices,
idle timeouts and node budgets are copied from `pkg/api/limits.go` at the top
of `freeze_sim.py`; per-tier latencies are the n2 measurements in ADR-906.
