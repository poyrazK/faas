#!/usr/bin/env python3
"""Evaluate the SSD platform-wake release gate from correlated NDJSON rows."""

from __future__ import annotations

import argparse
import json
import math
import statistics
import sys
from pathlib import Path
from typing import Any


def percentile(values: list[float], quantile: float) -> float:
    """Return a conservative nearest-rank percentile."""
    if not values:
        raise ValueError("percentile requires at least one value")
    ordered = sorted(values)
    rank = max(1, math.ceil(quantile * len(ordered)))
    return ordered[rank - 1]


def summary(values: list[float]) -> dict[str, float | int]:
    return {
        "samples": len(values),
        "min_ms": round(min(values), 3),
        "average_ms": round(statistics.fmean(values), 3),
        "p50_ms": round(percentile(values, 0.50), 3),
        "p90_ms": round(percentile(values, 0.90), 3),
        "p95_ms": round(percentile(values, 0.95), 3),
        "p99_ms": round(percentile(values, 0.99), 3),
        "max_ms": round(max(values), 3),
    }


def number(row: dict[str, Any], *keys: str) -> float | None:
    for key in keys:
        value = row.get(key)
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            return float(value)
    return None


def restore_ms(row: dict[str, Any]) -> float | None:
    direct = number(row, "restore_breakdown_total_ms", "restore_total_ms", "vm_restore_ms")
    if direct is not None:
        return direct
    breakdown = row.get("restore_breakdown")
    if isinstance(breakdown, dict):
        return number(breakdown, "total_ms")
    return None


def eligible(row: dict[str, Any], node_id: str) -> bool:
    if number(row, "http_code", "status") not in (None, 200.0):
        return False
    if str(row.get("method", "restore")) != "restore":
        return False
    if number(row, "queued_count") not in (None, 0.0):
        return False
    if number(row, "restore_gate_wait_ms") not in (None, 0.0):
        return False
    if node_id and str(row.get("node_id", "")) != node_id:
        return False
    return True


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Gate successful, zero-queue SSD restore samples using the platform-only "
            "admission/boot-start to first-upstream-byte boundary."
        )
    )
    parser.add_argument("input", type=Path, help="correlated wake samples as one JSON object per line")
    parser.add_argument("--node-id", default="", help="require this reference SSD compute node id")
    parser.add_argument("--min-samples", type=int, default=100)
    parser.add_argument("--platform-p95-max-ms", type=float, default=350.0)
    parser.add_argument(
        "--require-runtime",
        action="append",
        default=[],
        help="runtime class that must appear in the accepted cohort; repeat as needed",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    platform: list[float] = []
    restore: list[float] = []
    runtimes: dict[str, int] = {}
    malformed = 0
    with args.input.open(encoding="utf-8") as handle:
        for line in handle:
            if not line.strip():
                continue
            try:
                row = json.loads(line)
            except json.JSONDecodeError:
                malformed += 1
                continue
            if not isinstance(row, dict) or not eligible(row, args.node_id):
                continue
            full = number(row, "platform_ms", "platform_wake_ms", "boot_start_to_first_byte_ms")
            restored = restore_ms(row)
            if full is None or restored is None or full < 0 or restored < 0:
                continue
            platform.append(full)
            restore.append(restored)
            runtime = str(row.get("runtime", row.get("runtime_class", ""))).strip().lower()
            if runtime:
                runtimes[runtime] = runtimes.get(runtime, 0) + 1

    failures: list[str] = []
    if len(platform) < args.min_samples:
        failures.append(f"accepted samples {len(platform)} < required {args.min_samples}")
    missing_runtimes = sorted({value.lower() for value in args.require_runtime} - set(runtimes))
    if missing_runtimes:
        failures.append("missing required runtimes: " + ", ".join(missing_runtimes))

    report: dict[str, Any] = {
        "gate": "pass",
        "boundary": "platform admission/boot start to first upstream byte",
        "filters": {
            "node_id": args.node_id or "any",
            "method": "restore",
            "queued_count": 0,
            "restore_gate_wait_ms": 0,
            "http_code": 200,
        },
        "thresholds": {
            "minimum_samples": args.min_samples,
            "platform_p95_strictly_below_ms": args.platform_p95_max_ms,
        },
        "malformed_rows": malformed,
        "runtime_samples": dict(sorted(runtimes.items())),
    }
    if platform:
        report["platform_wake"] = summary(platform)
        report["vm_restore"] = summary(restore)
        if float(report["platform_wake"]["p95_ms"]) >= args.platform_p95_max_ms:
            failures.append(
                f"platform p95 {report['platform_wake']['p95_ms']} ms is not below "
                f"{args.platform_p95_max_ms} ms"
            )
    if failures:
        report["gate"] = "fail"
        report["failures"] = failures
    print(json.dumps(report, indent=2, sort_keys=True))
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
