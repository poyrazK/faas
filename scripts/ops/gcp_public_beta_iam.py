#!/usr/bin/env python3
"""Small deterministic IAM policy transformations for the GCP runbook."""

from __future__ import annotations

import argparse
import json
from pathlib import Path


AUDIT_SERVICES = ("storage.googleapis.com", "iam.googleapis.com")
AUDIT_TYPES = ("DATA_READ", "DATA_WRITE")


def add_audit_logs(policy: dict) -> dict:
    configs = {item.get("service"): item for item in policy.setdefault("auditConfigs", [])}
    for service in AUDIT_SERVICES:
        entry = configs.get(service)
        if entry is None:
            entry = {"service": service, "auditLogConfigs": []}
            policy["auditConfigs"].append(entry)
        existing = {item.get("logType") for item in entry.setdefault("auditLogConfigs", [])}
        for log_type in AUDIT_TYPES:
            if log_type not in existing:
                entry["auditLogConfigs"].append({"logType": log_type})
        entry["auditLogConfigs"].sort(key=lambda item: item["logType"])
    policy["auditConfigs"].sort(key=lambda item: item.get("service", ""))
    return policy


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("add-audit-logs",))
    parser.add_argument("source", type=Path)
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    policy = json.loads(args.source.read_text())
    args.destination.write_text(json.dumps(add_audit_logs(policy), indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
