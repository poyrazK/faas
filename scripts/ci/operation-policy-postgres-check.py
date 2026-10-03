#!/usr/bin/env python3
"""Require actual PostgreSQL policy retirement, races and history preservation."""
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]


def main():
    if not os.environ.get("DATABASE_URL") or os.environ.get("FAAS_SKIP_PG_TESTS"):
        print("operation-policy-postgres-check: DATABASE_URL required and FAAS_SKIP_PG_TESTS must be unset", file=sys.stderr)
        return 1
    required = {"TestExclusivePolicyRetirement", "TestExclusivePolicyRetirement/postgres"}
    passed = set()
    rejected = False
    command = ["go", "test", "-race", "-json", "-count=1", "-timeout=5m",
               "-run", "^TestExclusivePolicyRetirement$/^postgres$", "./pkg/state"]
    with subprocess.Popen(command, cwd=ROOT, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, text=True) as process:
        for line in process.stdout:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                print(line, end="", flush=True)
                continue
            action, test = event.get("Action"), event.get("Test")
            if action == "pass" and test in required:
                passed.add(test)
            if action in ("skip", "fail"):
                rejected = True
                print(f"operation-policy-postgres-check: {action}: {test}", file=sys.stderr)
            if event.get("Output"):
                print(event["Output"], end="", flush=True)
        code = process.wait()
    for test in sorted(required - passed):
        print(f"operation-policy-postgres-check: required pass missing: {test}", file=sys.stderr)
    if code or rejected or passed != required:
        return 1
    print("operation-policy-postgres-check: PostgreSQL retirement, binding/admission races, retained keys/history and active-quota reuse passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
