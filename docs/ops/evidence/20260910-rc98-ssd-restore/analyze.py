#!/usr/bin/env python3
import collections
import csv
import json
import math
import statistics
import subprocess
import sys
from datetime import datetime

CLI = "/opt/homebrew/bin/gregale"
EXPECTED_NODE = "cc461882-cba6-4182-8f26-e67c314361dc"

src = sys.argv[1]
evidence_path = sys.argv[2] if len(sys.argv) > 2 else "/private/tmp/rc98-ssd-platform-events.ndjson"
summary_path = sys.argv[3] if len(sys.argv) > 3 else "/private/tmp/rc98-ssd-summary.json"
rows = []
source_counts = collections.Counter()
node_counts = collections.Counter()


def parse_time(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def percentile(values, quantile):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * quantile) - 1)]


def stats(values):
    return {
        "min": min(values),
        "avg": statistics.fmean(values),
        "p50": percentile(values, 0.50),
        "p90": percentile(values, 0.90),
        "p95": percentile(values, 0.95),
        "p99": percentile(values, 0.99),
        "max": max(values),
    }


with open(src, newline="", encoding="utf-8") as stream, open(
    evidence_path, "w", encoding="utf-8"
) as evidence:
    for input_row in csv.DictReader(stream, delimiter="\t"):
        raw = subprocess.check_output(
            [CLI, "wake-timeline", input_row["app"], input_row["wake_id"], "--json"],
            text=True,
        )
        doc = json.loads(raw)
        events = doc["events"]
        start = next(
            event
            for event in events
            if event["kind"] == "wake.boot_started" and event["actor"] == "schedd"
        )
        done = next(
            event
            for event in events
            if event["kind"] == "wake.boot_completed" and event["actor"] == "schedd"
        )
        breakdown_event = next(
            event for event in events if event["kind"] == "wake.restore_breakdown"
        )
        breakdown = breakdown_event["data"]
        platform_ms = (
            parse_time(done["at"]) - parse_time(start["at"])
        ).total_seconds() * 1000
        for artifact in breakdown.get("resolve_artifacts", []):
            source_counts[(artifact["artifact"], artifact["source"])] += 1
        node_id = done["data"].get("node_id", "")
        node_counts[node_id] += 1
        result = {
            "sample": int(input_row["sample"]),
            "shape": input_row["shape"],
            "wave": int(input_row["wave"]),
            "app": input_row["app"],
            "wake_id": input_row["wake_id"],
            "platform_ms": platform_ms,
            "public_ms_context_only": float(input_row["request_ms_context_only"]),
            "http_code": int(input_row["http_code"]),
            "wake_header": input_row["wake_header"],
            "method": done["data"].get("method"),
            "node_id": node_id,
            "at_capacity": start["data"].get("at_capacity"),
            "concurrency_at_admit": start["data"].get("concurrency_at_admit"),
            "queued_count": start["data"].get("queued_count"),
            "restore_gate_wait_ms": breakdown.get("restore_gate_wait_ms", 0),
            "restore_breakdown": breakdown,
            "events": events,
        }
        evidence.write(json.dumps(result, separators=(",", ":")) + "\n")
        rows.append(result)


platform_values = [row["platform_ms"] for row in rows]
public_values = [row["public_ms_context_only"] for row in rows]
gate_values = [row["restore_gate_wait_ms"] for row in rows]
by_shape = {}
for shape in sorted({row["shape"] for row in rows}):
    shaped = [row["platform_ms"] for row in rows if row["shape"] == shape]
    by_shape[shape] = {"n": len(shaped), "platform_ms": stats(shaped)}
by_app = {}
for app in sorted({row["app"] for row in rows}):
    app_values = [row["platform_ms"] for row in rows if row["app"] == app]
    by_app[app] = {"n": len(app_values), "platform_ms": stats(app_values)}

summary = {
    "release_tag": "v0.1.18-rc.98",
    "release_commit": "76ff511e15d64df2ba703ad25a700381af8f3028",
    "project": "project-5ae37259-04cf-4070-bef",
    "node": "faas-compute-node-1",
    "node_id_counts": dict(node_counts),
    "disk": {"device": "sdb", "rotational": 0, "class": "pd-ssd"},
    "load_shape": "52 sequential restores plus 16 simultaneous three-app restore waves rotating across four runtimes; every request starts with its instance parked",
    "slo_interval": "schedd wake.boot_started.at -> schedd wake.boot_completed.at",
    "n": len(rows),
    "unique_wake_ids": len({row["wake_id"] for row in rows}),
    "platform_ms": stats(platform_values),
    "public_ms_context_only": stats(public_values),
    "restore_gate_wait_ms": stats(gate_values),
    "by_shape": by_shape,
    "by_app": by_app,
    "methods": sorted({row["method"] for row in rows}),
    "over_or_equal_350_ms": sum(value >= 350 for value in platform_values),
    "artifact_source_counts": {
        f"{artifact}:{source}": count
        for (artifact, source), count in sorted(source_counts.items())
    },
    "max_concurrency_at_admit": max(
        row["concurrency_at_admit"] or 0 for row in rows
    ),
    "max_queued_count": max(row["queued_count"] or 0 for row in rows),
}
with open(summary_path, "w", encoding="utf-8") as stream:
    json.dump(summary, stream, indent=2)
    stream.write("\n")
print(json.dumps(summary, indent=2))
print("slowest_ten:")
for row in sorted(rows, key=lambda item: item["platform_ms"], reverse=True)[:10]:
    print(
        f'{row["sample"]}\t{row["shape"]}\t{row["app"]}\t'
        f'{row["wake_id"]}\t{row["platform_ms"]:.3f}\t'
        f'{row["restore_gate_wait_ms"]}'
    )

assert len(rows) == 100, f"expected 100 samples, got {len(rows)}"
assert summary["unique_wake_ids"] == 100, "wake IDs must be unique"
assert summary["methods"] == ["restore"], summary["methods"]
assert summary["platform_ms"]["p95"] < 350, summary["platform_ms"]["p95"]
assert summary["node_id_counts"] == {EXPECTED_NODE: 100}, summary["node_id_counts"]
assert summary["artifact_source_counts"] == {
    "base:cache_hit": 100,
    "kernel:cache_hit": 100,
    "main:cache_hit": 100,
}, summary["artifact_source_counts"]
