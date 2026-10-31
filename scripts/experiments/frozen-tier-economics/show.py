"""Print one freeze_sim.py result file as a table.

Usage: python3 -P -E show.py out.json
"""
import json
import sys

with open(sys.argv[1]) as f:
    r = json.load(f)
cur = {'USD': '$', 'EUR': '€'}.get(r.get('currency', 'EUR'), '')
print(f"apps={r['apps']} invocations={r['invocations']:,} plans={r.get('plan_table', 'eur-2026-10-12')} "
      f"functions={r.get('functions', False)}")
for name, d in r['policies'].items():
    n = list(d['nodes'].values())
    print(f"{name[:62]:62s} GBh/app {d['billed_gbh_per_app_month_mean']:6.1f}  rev {cur}{d['revenue_per_app_month']:.3f}  "
          f"overage {cur}{d['overage_per_app_month']:.4f}  "
          f"wake {d['mean_wake_latency_ms']:6.1f} ms  paused {d['wake_tier_share_pct'].get('paused', 0.0):5.1f}%  "
          f"frozen {d['wake_tier_share_pct']['frozen']:5.1f}%  "
          f"EX44 {n[0]['apps_per_node']:5d} ({n[0]['binding']}, €{n[0]['infra_eur_per_app_month']})  "
          f"GCP {n[1]['apps_per_node']:4d} ({n[1]['binding']}, €{n[1]['infra_eur_per_app_month']})")
