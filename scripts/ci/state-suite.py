#!/usr/bin/env python3
"""ADR-375: execute every state test once, retaining race and coverage gates."""

import argparse
import concurrent.futures
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import subprocess
import time


def partitions(names):
    roots = [name for name in names if re.fullmatch(r"(?:Test|Example|Fuzz)[A-Za-z0-9_]*", name)]
    if not roots or len(roots) != len(set(roots)):
        raise ValueError("empty or duplicate runnable state test inventory")
    if any(not re.fullmatch(r"(?:Test|Example|Fuzz|Benchmark)[A-Za-z0-9_]*", name) for name in names):
        raise ValueError("unrecognized state test inventory entry")
    groups = [sorted(roots)[index::2] for index in range(2)]
    if any(not group for group in groups) or set(groups[0]) & set(groups[1]) or set().union(*map(set, groups)) != set(roots):
        raise ValueError("state test partitions are incomplete or overlap")
    return groups


def coverage(path):
    lines = path.read_text().splitlines()
    if not lines or lines[0] != "mode: atomic":
        raise ValueError(f"invalid atomic coverage profile: {path}")
    blocks = {}
    for line in lines[1:]:
        location, statements, count = line.split()
        value = (int(statements), int(count))
        if location in blocks or min(value) < 0:
            raise ValueError(f"invalid coverage block: {line}")
        blocks[location] = value
    if not blocks:
        raise ValueError(f"empty coverage profile: {path}")
    return blocks


def merged_coverage(state_profiles, other_profile):
    states = [coverage(path) for path in state_profiles]
    if set(states[0]) != set(states[1]):
        raise ValueError("state shards have different coverage block inventories")
    merged = {}
    for profile in states + [coverage(other_profile)]:
        for location, (statements, count) in profile.items():
            old_statements, old_count = merged.get(location, (statements, 0))
            if old_statements != statements:
                raise ValueError("coverage statement count changed between shards")
            merged[location] = (statements, old_count + count)
    return "mode: atomic\n" + "".join(f"{key} {value[0]} {value[1]}\n" for key, value in sorted(merged.items()))


def run(args, log, env):
    started = time.monotonic()
    with log.open("w") as stream:
        result = subprocess.run(args, stdout=stream, stderr=subprocess.STDOUT, env=env)
    return {"args": args, "exit_code": result.returncode, "seconds": time.monotonic() - started, "log": log.name}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--packages", required=True)
    parser.add_argument("--coverage", type=pathlib.Path, required=True)
    args = parser.parse_args()
    repo = pathlib.Path.cwd()
    output = args.coverage.parent / "state-suite"
    output.mkdir(parents=True, exist_ok=False)
    spec = importlib.util.spec_from_file_location("traffic_checks", pathlib.Path(__file__).with_name("traffic-platform-checks.py"))
    checks = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(checks)
    freeze = checks.source_snapshot(repo)
    checks.write_json(output / "source-freeze.json", freeze)
    if freeze["commit"] != os.environ.get("GITHUB_SHA") or not os.environ.get("DATABASE_URL") or os.environ.get("FAAS_SKIP_PG_TESTS"):
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
               "go_version": subprocess.check_output(["go", "version"], text=True, env=env).strip(),
               "state_runtime_environment": {key: env.get(key) for key in ("GOMAXPROCS", "CGO_ENABLED", "GOTOOLCHAIN", "GOFLAGS", "GOEXPERIMENT")}}
    try:
        build = run(["go", "test", "-race", "-c", "-p=1", "-cover", "-covermode=atomic", "-o", str(binary), state_package], output / "build.log", env)
        receipt["commands"].append(build)
        if build["exit_code"]:
            raise ValueError("state race binary build failed")
        receipt["binary_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
        inventory = subprocess.run([str(binary.resolve()), "-test.list=."] , capture_output=True, text=True, env=env)
        (output / "inventory.log").write_text(inventory.stdout + inventory.stderr)
        if inventory.returncode:
            raise ValueError("state test inventory failed")
        groups = partitions(inventory.stdout.splitlines())
        receipt["groups"] = groups
        def shard(index):
            selector = "^(" + "|".join(groups[index]) + ")$"
            command = [str(binary.resolve()), "-test.v", "-test.count=1", "-test.timeout=20m", "-test.run=" + selector,
                       "-test.coverprofile=" + str((output / f"state-{index}.out").resolve())]
            return run(command, output / f"state-{index}.log", env)
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            receipts = list(executor.map(shard, range(2)))
        receipt["commands"].extend(receipts)
        for index, result in enumerate(receipts):
            terminals = re.findall(r"^--- (PASS|FAIL|SKIP): ([A-Za-z0-9_]+) ", (output / f"state-{index}.log").read_text(), re.MULTILINE)
            if result["exit_code"] or any(status == "FAIL" for status, _ in terminals) or sorted(name for _, name in terminals) != sorted(groups[index]):
                raise ValueError(f"state shard {index} failed or has missing/duplicate test terminals")
        other = run(["go", "test", "-race", "-count=1", "-p=4", "-timeout=20m", "-covermode=atomic",
                     "-coverprofile=" + str(output / "others.out"), *others], output / "others.log", os.environ.copy())
        receipt["commands"].append(other)
        if other["exit_code"]:
            raise ValueError("remaining state shard packages failed")
        args.coverage.write_text(merged_coverage([output / f"state-{index}.out" for index in range(2)], output / "others.out"))
        receipt["result"] = "passed"
        print(f"state suite: {sum(map(len, groups))} runnable roots, two disjoint groups, full atomic coverage merged")
    finally:
        receipt["source_unchanged"] = checks.source_snapshot(repo) == freeze
        checks.write_json(output / "terminal.json", receipt)
        if not receipt["source_unchanged"]:
            raise ValueError("state suite source changed while tests were running")


if __name__ == "__main__":
    main()
