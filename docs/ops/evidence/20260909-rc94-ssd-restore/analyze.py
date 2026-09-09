import collections
import csv
import json
import math
import statistics
import subprocess
import sys
from datetime import datetime

src = sys.argv[1]
evidence_path = sys.argv[2] if len(sys.argv) > 2 else "/private/tmp/rc94-ssd-platform-events.ndjson"
summary_path = sys.argv[3] if len(sys.argv) > 3 else "/private/tmp/rc94-ssd-summary.json"
rows = []
source_counts = collections.Counter()
node_counts = collections.Counter()

def parse_time(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))

with open(src, newline="") as f, open(evidence_path, "w") as evidence:
    for cycle, wake_id, public_s in csv.reader(f, delimiter="\t"):
        raw = subprocess.check_output(
            ["gregale", "wake-timeline", "public-val-go-persistent-0906", wake_id, "--json"],
            text=True,
        )
        doc = json.loads(raw)
        events = doc["events"]
        start = next(e for e in events if e["kind"] == "wake.boot_started" and e["actor"] == "schedd")
        done = next(e for e in events if e["kind"] == "wake.boot_completed" and e["actor"] == "schedd")
        breakdown_event = next(e for e in events if e["kind"] == "wake.restore_breakdown")
        breakdown = breakdown_event["data"]
        platform_ms = (parse_time(done["at"]) - parse_time(start["at"])).total_seconds() * 1000
        artifacts = breakdown.get("resolve_artifacts", [])
        for artifact in artifacts:
            source_counts[(artifact["artifact"], artifact["source"])] += 1
        node_id = done["data"].get("node_id", "")
        node_counts[node_id] += 1
        result = {
            "cycle": int(cycle),
            "wake_id": wake_id,
            "platform_ms": platform_ms,
            "public_ms": float(public_s) * 1000,
            "method": done["data"].get("method"),
            "node_id": node_id,
            "at_capacity": start["data"].get("at_capacity"),
            "concurrency_at_admit": start["data"].get("concurrency_at_admit"),
            "queued_count": start["data"].get("queued_count"),
            "restore_breakdown": breakdown,
            "events": events,
        }
        evidence.write(json.dumps(result, separators=(",", ":")) + "\n")
        rows.append(result)

def percentile(values, quantile):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * quantile) - 1)]

values = [row["platform_ms"] for row in rows]
public_values = [row["public_ms"] for row in rows]
summary = {
    "release_tag": "v0.1.18-rc.94",
    "release_commit": "86968d3d8d39670cc553f397358b70938bed49bc",
    "project": "project-5ae37259-04cf-4070-bef",
    "node": "faas-compute-node-1",
    "node_id_counts": dict(node_counts),
    "disk": {"device": "sdb", "rotational": 0, "class": "pd-ssd"},
    "app": "public-val-go-persistent-0906",
    "load_shape": "100 sequential public requests; normal idle reaper parked the sole instance before each request",
    "slo_interval": "schedd wake.boot_started.at -> schedd wake.boot_completed.at",
    "n": len(rows),
    "unique_wake_ids": len({row["wake_id"] for row in rows}),
    "platform_ms": {
        "min": min(values),
        "avg": statistics.fmean(values),
        "p50": percentile(values, 0.50),
        "p90": percentile(values, 0.90),
        "p95": percentile(values, 0.95),
        "p99": percentile(values, 0.99),
        "max": max(values),
    },
    "public_ms_context_only": {
        "min": min(public_values),
        "avg": statistics.fmean(public_values),
        "p50": percentile(public_values, 0.50),
        "p90": percentile(public_values, 0.90),
        "p95": percentile(public_values, 0.95),
        "p99": percentile(public_values, 0.99),
        "max": max(public_values),
    },
    "methods": sorted({row["method"] for row in rows}),
    "over_or_equal_350_ms": sum(value >= 350 for value in values),
    "artifact_source_counts": {f"{artifact}:{source}": count for (artifact, source), count in sorted(source_counts.items())},
    "max_concurrency_at_admit": max(row["concurrency_at_admit"] or 0 for row in rows),
    "max_queued_count": max(row["queued_count"] or 0 for row in rows),
}
with open(summary_path, "w") as f:
    json.dump(summary, f, indent=2)
    f.write("\n")
print(json.dumps(summary, indent=2))
print("slowest_five:")
for row in sorted(rows, key=lambda item: item["platform_ms"], reverse=True)[:5]:
    print(f'{row["cycle"]}\t{row["wake_id"]}\t{row["platform_ms"]:.3f}\t{row["public_ms"]:.3f}')

assert len(rows) == 100, f"expected 100 samples, got {len(rows)}"
assert summary["unique_wake_ids"] == 100, "wake IDs must be unique"
assert summary["methods"] == ["restore"], summary["methods"]
assert summary["over_or_equal_350_ms"] == 0, summary["over_or_equal_350_ms"]
assert summary["platform_ms"]["p95"] < 350, summary["platform_ms"]["p95"]
assert summary["node_id_counts"] == {"cc461882-cba6-4182-8f26-e67c314361dc": 100}, summary["node_id_counts"]
assert summary["artifact_source_counts"] == {
    "base:cache_hit": 100,
    "kernel:cache_hit": 100,
    "main:cache_hit": 100,
}, summary["artifact_source_counts"]
