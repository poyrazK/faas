#!/usr/bin/env python3
"""Validate MCP release evidence; never deploy or contact an external service."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

REQUIRED = {
    "untouched_lockfile_deploy", "native_cold_boot", "native_park_restore",
    "oauth_chatgpt_login", "oauth_codex_login", "official_sdk_interop",
    "tasks_restart_replicas", "tasks_scale_from_zero",
}


def check(path, commit):
    root = path.parent.resolve()
    evidence = json.loads(path.read_text())
    if evidence.get("version") != 1 or evidence.get("commit") != commit:
        raise ValueError("evidence must be version 1 and match the reviewed source commit")
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("reviewed source must be a full commit SHA")
    records = evidence.get("checks", [])
    by_name = {record["name"]: record for record in records}
    if len(by_name) != len(records) or set(by_name) != REQUIRED:
        raise ValueError("provide exactly one evidence record for every required qualification")
    for name, record in by_name.items():
        if record.get("status") != "passed" or not record.get("target"):
            raise ValueError(f"{name}: qualification is incomplete or has no test target")
        relative = Path(record["artifact"])
        artifact = (root / relative).resolve()
        if relative.is_absolute() or not artifact.is_relative_to(root) or not artifact.is_file():
            raise ValueError(f"{name}: artifact must be a file within the evidence directory")
        if not re.fullmatch(r"[0-9a-f]{64}", record.get("sha256", "")):
            raise ValueError(f"{name}: artifact SHA-256 is required")
        if hashlib.sha256(artifact.read_bytes()).hexdigest() != record["sha256"]:
            raise ValueError(f"{name}: artifact digest does not match")
    return {"ok": True, "commit": commit, "checks": sorted(by_name)}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", type=Path)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args()
    try:
        print(json.dumps(check(args.evidence, args.commit)))
    except (ValueError, KeyError, TypeError, OSError) as error:
        print(json.dumps({"ok": False, "error": str(error)}))
        sys.exit(1)
