#!/usr/bin/env python3
"""ADR-570: require every state partition before applying full coverage gates."""
import argparse
import hashlib
import importlib.util
import json
import os
import pathlib


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, pathlib.Path(__file__).with_name(filename))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


suite = module("state_suite", "state-suite.py")
checks = module("traffic_checks", "traffic-platform-checks.py")


def verify_remaining_command(command, packages, profile):
    expected = ["go", "test", "-v", "-race", "-count=1", "-p=4", "-timeout=30m", "-covermode=atomic",
                suite.parity_coverage_arg(packages), "-coverprofile=" + str(profile), *packages]
    if command != expected:
        raise ValueError("remaining package scope or flags changed")


def verify_inventory(receipts, count=8):
    if len(receipts) != count or sorted(item["partition"] for item in receipts) != list(range(count)):
        raise ValueError("state partition receipts are missing or duplicated")
    first = receipts[0]
    shared = ("commit", "packages", "groups", "go_version", "state_runtime_environment")
    roots = [name for group in first["groups"] for name in group]
    if first["groups"] != suite.partitions(roots, count):
        raise ValueError("state test inventory is not exhaustive and disjoint")
    for item in receipts:
        if item.get("result") != "passed" or not item.get("source_unchanged") or item["partition_count"] != count:
            raise ValueError("failed, incomplete or changed-source state partition")
        if item["state_runtime_environment"].get("GREGALE_GITOPS_ACCEPTANCE") != "1":
            raise ValueError("GitOps PostgreSQL acceptance was not enabled")
        if any(item[key] != first[key] for key in shared):
            raise ValueError("state source, runtime, package scope or inventory changed between partitions")
    return first["groups"]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--artifacts", type=pathlib.Path, required=True)
    parser.add_argument("--coverage", type=pathlib.Path, required=True)
    args = parser.parse_args()
    args.coverage.parent.mkdir(parents=True, exist_ok=True)
    freeze = checks.source_snapshot(pathlib.Path.cwd())
    verdict = {"commit": freeze["commit"], "native_executions": 0,
               "acceptance": "Full state software coverage only; native/deployed/release qualification remains open."}
    try:
        paths = sorted(args.artifacts.rglob("terminal.json"))
        receipts = [json.loads(path.read_text()) for path in paths]
        groups = verify_inventory(receipts)
        if freeze["commit"] != os.environ.get("GITHUB_SHA") or receipts[0]["commit"] != freeze["commit"]:
            raise ValueError("coverage does not belong to the dispatched source")
        if receipts[0]["go_version"] != "go version go1.25.13 linux/amd64":
            raise ValueError("state coverage toolchain changed")
        profiles = []
        other = None
        for path, item in zip(paths, receipts):
            root = path.parent
            if json.loads((root / "source-freeze.json").read_text()) != freeze:
                raise ValueError("state artifact does not match every source file")
            if hashlib.sha256((root / "state.test").read_bytes()).hexdigest() != item["binary_sha256"]:
                raise ValueError("state binary hash changed")
            inventory = (root / "inventory.log").read_text().splitlines()
            if suite.partitions(inventory, 8) != groups:
                raise ValueError("compiled state test inventory changed")
            commands = item["commands"]
            if len(commands) != (3 if item["partition"] == 0 else 2) or any(cmd["exit_code"] for cmd in commands):
                raise ValueError("state command scope is incomplete or failed")
            if commands[0]["args"][:7] != ["go", "test", "-race", "-c", "-p=1", "-cover", "-covermode=atomic"]:
                raise ValueError("state race/coverage build flags changed")
            expected = groups[item["partition"]]
            run = commands[1]["args"]
            if run[1:5] != ["-test.v", "-test.count=1", "-test.timeout=30m", "-test.run=^(" + "|".join(expected) + ")$"]:
                raise ValueError("state test count, deadline or selector changed")
            if not commands[1]["cwd"].endswith("/pkg/state"):
                raise ValueError("state tests did not retain their package working directory")
            suite.validate_terminals(expected, (root / "state.log").read_text())
            profiles.append(root / "state.out")
            if item["partition"] == 0:
                others = [pkg for pkg in item["packages"] if pkg != "github.com/onebox-faas/faas/pkg/state"]
                other = root / "others.out"
                # The profile path is absolute in the producer's retained command.
                producer_profile = pathlib.Path(commands[1]["args"][5].removeprefix("-test.coverprofile="))
                verify_remaining_command(commands[2]["args"], others, producer_profile.with_name("others.out"))
        args.coverage.write_text(suite.merged_coverage(profiles, other))
        verdict.update(result="passed", roots=sum(map(len, groups)), partitions=8,
                       coverage_sha256=hashlib.sha256(args.coverage.read_bytes()).hexdigest(),
                       binary_hashes={str(item["partition"]): item["binary_sha256"] for item in receipts})
        print(f"verified every one of {verdict['roots']} state roots across eight runners; full atomic coverage merged")
    except Exception as error:
        verdict.update(result="failed", error=str(error))
        raise
    finally:
        verdict["source_unchanged"] = checks.source_snapshot(pathlib.Path.cwd()) == freeze
        checks.write_json(args.coverage.parent / "state-coverage-terminal.json", verdict)
        if not verdict["source_unchanged"]:
            raise ValueError("coverage aggregation changed source")


if __name__ == "__main__":
    main()
