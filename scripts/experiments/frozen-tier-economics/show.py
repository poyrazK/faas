"""Print one freeze_sim.py result file as a table.

Usage: python3 -P -E show.py out.json
"""
import json
import sys

with open(sys.argv[1]) as f:
    r = json.load(f)
print(f"apps={r['apps']} invocations={r['invocations']:,}")
for name, d in r['policies'].items():
    n = list(d['nodes'].values())
    print(f"{name[:60]:60s} GBh/app {d['billed_gbh_per_app_month_mean']:6.1f}  rev €{d['revenue_eur_per_app_month']:.2f}  "
          f"wake {d['mean_wake_latency_ms']:6.1f} ms  paused {d['wake_tier_share_pct'].get('paused', 0.0):5.1f}%  "
          f"frozen {d['wake_tier_share_pct']['frozen']:5.1f}%  "
          f"EX44 {n[0]['apps_per_node']:5d} ({n[0]['binding']}, €{n[0]['infra_eur_per_app_month']})  "
          f"GCP {n[1]['apps_per_node']:4d} ({n[1]['binding']}, €{n[1]['infra_eur_per_app_month']})")
