#!/usr/bin/env python3
"""ADR-570: execute every state test once, retaining race and coverage gates."""

import argparse
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import subprocess
import time


def partitions(names, count=2):
    roots = [name for name in names if re.fullmatch(r"(?:Test|Example|Fuzz)[A-Za-z0-9_]*", name)]
    if not roots or len(roots) != len(set(roots)):
        raise ValueError("empty or duplicate runnable state test inventory")
    if any(not re.fullmatch(r"(?:Test|Example|Fuzz|Benchmark)[A-Za-z0-9_]*", name) for name in names):
        raise ValueError("unrecognized state test inventory entry")
    if count < 2:
        raise ValueError("state suite requires multiple exhaustive partitions")
    groups = [sorted(roots)[index::count] for index in range(count)]
    if any(not group for group in groups) or sum(map(len, groups)) != len(set().union(*map(set, groups))) or set().union(*map(set, groups)) != set(roots):
        raise ValueError("state test partitions are incomplete or overlap")
    return groups


def coverage(path, allow_duplicates=False):
    lines = path.read_text().splitlines()
    if not lines or lines[0] != "mode: atomic":
        raise ValueError(f"invalid atomic coverage profile: {path}")
    blocks = {}
    for line in lines[1:]:
        location, statements, count = line.split()
        value = (int(statements), int(count))
        if min(value) < 0:
            raise ValueError(f"invalid coverage block: {line}")
        if location in blocks:
            prior_statements, prior_count = blocks[location]
            if not allow_duplicates or prior_statements != value[0]:
                raise ValueError(f"invalid or incompatible repeated coverage block: {line}")
            value = (prior_statements, prior_count + value[1])
        blocks[location] = value
    if not blocks:
        raise ValueError(f"empty coverage profile: {path}")
    return blocks


def merged_coverage(state_profiles, other_profile):
    states = [coverage(path) for path in state_profiles]
    if len(states) < 2 or any(set(states[0]) != set(profile) for profile in states[1:]):
        raise ValueError("state shards have different coverage block inventories")
    merged = {}
    # External adapter binaries repeat instrumented state locations. Count
    # each statement once and union their hits, retaining strict state inventories.
    for profile in states + [coverage(other_profile, allow_duplicates=True)]:
        for location, (statements, count) in profile.items():
            old_statements, old_count = merged.get(location, (statements, 0))
            if old_statements != statements:
                raise ValueError("coverage statement count changed between shards")
            merged[location] = (statements, old_count + count)
    return "mode: atomic\n" + "".join(f"{key} {value[0]} {value[1]}\n" for key, value in sorted(merged.items()))


def parity_coverage_arg(packages):
    state_package = "github.com/onebox-faas/faas/pkg/state"
    return "-coverpkg=" + ",".join(sorted({state_package, *packages} - {state_package + "/sqlc"}))


def run(args, log, env, cwd=None):
    started = time.monotonic()
    with log.open("w") as stream:
        result = subprocess.run(args, stdout=stream, stderr=subprocess.STDOUT, env=env, cwd=cwd)
    return {"args": args, "exit_code": result.returncode, "seconds": time.monotonic() - started, "log": log.name, "cwd": str(cwd or pathlib.Path.cwd())}


def validate_terminals(names, log):
    terminals = re.findall(r"^--- (PASS|FAIL|SKIP): ([A-Za-z0-9_]+) ", log, re.MULTILINE)
    if any(status == "FAIL" for status, _ in terminals) or sorted(name for _, name in terminals) != sorted(names):
        raise ValueError("missing, failed or duplicate state test terminals")


def list_inventory(binary, output, env, cwd):
    result = subprocess.run([str(binary), "-test.list=."], capture_output=True, text=True, env=env, cwd=cwd)
    (output / "inventory.log").write_text(result.stdout)
    (output / "inventory.stderr").write_text(result.stderr)
    if result.returncode:
        raise ValueError("state test inventory failed")
    return result.stdout.splitlines()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--packages", required=True)
    parser.add_argument("--coverage", type=pathlib.Path, required=True)
    parser.add_argument("--partition", type=int, required=True)
    parser.add_argument("--partitions", type=int, default=8)
    args = parser.parse_args()
    if args.partitions != 8 or not 0 <= args.partition < args.partitions:
        raise ValueError("state CI requires all eight independently scheduled partitions")
    repo = pathlib.Path.cwd()
    output = (args.coverage.parent / "state-suite" / str(args.partition)).resolve()
    output.mkdir(parents=True, exist_ok=False)
    spec = importlib.util.spec_from_file_location("traffic_checks", pathlib.Path(__file__).with_name("traffic-platform-checks.py"))
    checks = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(checks)
    freeze = checks.source_snapshot(repo)
    checks.write_json(output / "source-freeze.json", freeze)
    if freeze["commit"] != os.environ.get("GITHUB_SHA") or not os.environ.get("DATABASE_URL") or os.environ.get("FAAS_SKIP_PG_TESTS") or os.environ.get("GREGALE_GITOPS_ACCEPTANCE") != "1":
        raise ValueError("state suite requires the dispatched commit and PostgreSQL")
    packages = args.packages.split()
    state_package = "github.com/onebox-faas/faas/pkg/state"
    if packages.count(state_package) != 1 or len(packages) != len(set(packages)):
        raise ValueError("changed or duplicated state package scope")
    others = [package for package in packages if package != state_package]
    if not others:
        raise ValueError("remaining package scope cannot be empty")
    env = os.environ.copy()
    env["GOMAXPROCS"] = "2"
    binary = output / "state.test"
    receipt = {"commit": freeze["commit"], "packages": packages, "commands": [], "native_executions": 0,
               "partition": args.partition, "partition_count": args.partitions,
               "go_version": subprocess.check_output(["go", "version"], text=True, env=env).strip(),
               "state_runtime_environment": {key: env.get(key) for key in ("GOMAXPROCS", "CGO_ENABLED", "GOTOOLCHAIN", "GOFLAGS", "GOEXPERIMENT", "GREGALE_GITOPS_ACCEPTANCE")}}
    try:
        build = run(["go", "test", "-race", "-c", "-p=1", "-cover", "-covermode=atomic", "-o", str(binary), state_package], output / "build.log", env)
        receipt["commands"].append(build)
        if build["exit_code"]:
            raise ValueError("state race binary build failed")
        receipt["binary_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
        inventory = list_inventory(binary, output, env, repo / "pkg/state")
        groups = partitions(inventory, args.partitions)
        receipt["groups"] = groups
        selected = groups[args.partition]
        selector = "^(" + "|".join(selected) + ")$"
        command = [str(binary), "-test.v", "-test.count=1", "-test.timeout=30m", "-test.run=" + selector,
                   "-test.coverprofile=" + str(output / "state.out")]
        result = run(command, output / "state.log", env, repo / "pkg/state")
        receipt["commands"].append(result)
        if result["exit_code"]:
            raise ValueError(f"state partition {args.partition} failed")
        validate_terminals(selected, (output / "state.log").read_text())
        if args.partition == 0:
            other = run(["go", "test", "-race", "-count=1", "-p=4", "-timeout=30m", "-covermode=atomic",
                         parity_coverage_arg(others), "-coverprofile=" + str(output / "others.out"), *others], output / "others.log", os.environ.copy())
            receipt["commands"].append(other)
            if other["exit_code"]:
                raise ValueError("remaining state shard packages failed")
        receipt["result"] = "passed"
        print(f"state partition {args.partition}: {len(selected)} of {sum(map(len, groups))} roots; full coverage awaits all eight receipts")
    finally:
        receipt["source_unchanged"] = checks.source_snapshot(repo) == freeze
        checks.write_json(output / "terminal.json", receipt)
        if not receipt["source_unchanged"]:
            raise ValueError("state suite source changed while tests were running")


if __name__ == "__main__":
    main()
