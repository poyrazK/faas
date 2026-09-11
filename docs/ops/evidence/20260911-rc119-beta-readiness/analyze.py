#!/usr/bin/env python3
"""Rebuild an rc.119 SSD restore cohort from canonical timelines."""

import collections
import argparse
import csv
import json
import math
import statistics
import subprocess
import time
from datetime import datetime
from pathlib import Path


CLI = "/opt/homebrew/bin/gregale"
APP = "gregale-api-demo"
EXPECTED_NODE = "cc461882-cba6-4182-8f26-e67c314361dc"
EXPECTED_RELEASE = "bca75d649ffabe5d40c466c246ed60dacad3bcc6"


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


root = Path(__file__).resolve().parent
parser = argparse.ArgumentParser()
parser.add_argument("--correlation", type=Path, default=root / "request-correlation.tsv")
parser.add_argument("--vmmd", type=Path, default=root / "vmmd-wake-ok.ndjson")
parser.add_argument("--events", type=Path, default=root / "platform-events.ndjson")
parser.add_argument("--summary", type=Path, default=root / "summary.json")
parser.add_argument("--expected-n", type=int, default=100)
parser.add_argument("--fixture", default=APP)
parser.add_argument(
    "--load-shape",
    default="100 sequential explicit park-to-restore cycles; one public HTTP 200 verification after every restore",
)
args = parser.parse_args()


def fetch_timeline(app, wake_id):
    last_error = ""
    for attempt in range(10):
        proc = subprocess.run(
            [CLI, "wake-timeline", app, wake_id, "--json"],
            text=True,
            capture_output=True,
        )
        if proc.returncode == 0:
            return json.loads(proc.stdout)
        last_error = proc.stderr.strip()
        time.sleep(min(0.5 * (attempt + 1), 3))
    raise RuntimeError(f"could not fetch {app} wake {wake_id}: {last_error}")


correlation_path = args.correlation
vmmd_path = args.vmmd
events_path = args.events
summary_path = args.summary

with vmmd_path.open(encoding="utf-8") as stream:
    vmmd_rows = [json.loads(line) for line in stream if line.strip()]
vmmd_by_wake = {row["wake_id"]: row for row in vmmd_rows}

rows = []
source_counts = collections.Counter()
node_counts = collections.Counter()
version_counts = collections.Counter()
status_counts = collections.Counter()

with correlation_path.open(newline="", encoding="utf-8") as stream, events_path.open(
    "w", encoding="utf-8"
) as evidence:
    for input_row in csv.DictReader(stream, delimiter="\t"):
        wake_id = input_row["wake_id"]
        doc = fetch_timeline(input_row["app"], wake_id)
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
        platform_ms = (parse_time(done["at"]) - parse_time(start["at"])).total_seconds() * 1000
        vmmd = vmmd_by_wake[wake_id]

        for artifact in breakdown.get("resolve_artifacts", []):
            source_counts[(artifact["artifact"], artifact["source"])] += 1
        node_id = done["data"].get("node_id", "")
        node_counts[node_id] += 1
        version_counts[vmmd.get("version", "")] += 1
        status_counts[input_row["http_code"]] += 1

        result = {
            "sample": int(input_row["sample"]),
            "shape": input_row["shape"],
            "app": input_row["app"],
            "wake_id": wake_id,
            "instance_id": input_row.get("instance_id", ""),
            "http_code": int(input_row["http_code"]),
            "platform_ms": platform_ms,
            "vmmd_total_ms": vmmd["total_ms"],
            "restore_breakdown_total_ms": breakdown["total_ms"],
            "method": done["data"].get("method"),
            "node_id": node_id,
            "at_capacity": start["data"].get("at_capacity"),
            "concurrency_at_admit": start["data"].get("concurrency_at_admit"),
            "queued_count": start["data"].get("queued_count"),
            "restore_gate_wait_ms": breakdown.get("restore_gate_wait_ms", 0),
            "release_commit": vmmd.get("version"),
            "restore_breakdown": breakdown,
            "events": events,
        }
        evidence.write(json.dumps(result, separators=(",", ":")) + "\n")
        rows.append(result)


platform_values = [row["platform_ms"] for row in rows]
vmmd_values = [row["vmmd_total_ms"] for row in rows]
breakdown_values = [row["restore_breakdown_total_ms"] for row in rows]
gate_values = [row["restore_gate_wait_ms"] for row in rows]

summary = {
    "release_tag": "v0.1.18-rc.119",
    "release_commit": EXPECTED_RELEASE,
    "project": "project-5ae37259-04cf-4070-bef",
    "node": "faas-compute-node-1",
    "node_id_counts": dict(node_counts),
    "disk": {"device": "sdb", "rotational": 0, "class": "pd-ssd"},
    "fixture": args.fixture,
    "load_shape": args.load_shape,
    "slo_interval": "schedd wake.boot_started.at -> schedd wake.boot_completed.at",
    "n": len(rows),
    "unique_wake_ids": len({row["wake_id"] for row in rows}),
    "platform_ms": stats(platform_values),
    "vmmd_total_ms": stats(vmmd_values),
    "restore_breakdown_total_ms": stats(breakdown_values),
    "restore_gate_wait_ms": stats(gate_values),
    "methods": sorted({row["method"] for row in rows}),
    "http_status_counts": dict(status_counts),
    "release_counts": dict(version_counts),
    "over_or_equal_350_ms": sum(value >= 350 for value in platform_values),
    "artifact_source_counts": {
        f"{artifact}:{source}": count
        for (artifact, source), count in sorted(source_counts.items())
    },
    "max_concurrency_at_admit": max(row["concurrency_at_admit"] or 0 for row in rows),
    "max_queued_count": max(row["queued_count"] or 0 for row in rows),
}

with summary_path.open("w", encoding="utf-8") as stream:
    json.dump(summary, stream, indent=2)
    stream.write("\n")

print(json.dumps(summary, indent=2))
print("slowest_ten:")
for row in sorted(rows, key=lambda item: item["platform_ms"], reverse=True)[:10]:
    print(
        f'{row["sample"]}\t{row["wake_id"]}\t{row["platform_ms"]:.3f}\t'
        f'{row["vmmd_total_ms"]}\t{row["restore_breakdown_total_ms"]}'
    )

assert len(rows) == args.expected_n, f"expected {args.expected_n} samples, got {len(rows)}"
assert summary["unique_wake_ids"] == args.expected_n, "wake IDs must be unique"
assert summary["methods"] == ["restore"], summary["methods"]
assert summary["platform_ms"]["p95"] < 350, summary["platform_ms"]["p95"]
assert summary["node_id_counts"] == {EXPECTED_NODE: args.expected_n}, summary["node_id_counts"]
assert summary["http_status_counts"] == {"200": args.expected_n}, summary["http_status_counts"]
assert summary["release_counts"] == {EXPECTED_RELEASE: args.expected_n}, summary["release_counts"]
assert summary["artifact_source_counts"] == {
    "base:cache_hit": args.expected_n,
    "kernel:cache_hit": args.expected_n,
    "main:cache_hit": args.expected_n,
}, summary["artifact_source_counts"]
