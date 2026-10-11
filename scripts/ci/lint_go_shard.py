#!/usr/bin/env python3
"""Run every configured Go lint rule with a bounded package working set."""

import argparse
import os
import subprocess
import sys


def select_packages(packages, module, shard, shards):
    if shards < 1 or not 1 <= shard <= shards:
        raise ValueError("shard must be between 1 and shards")
    inventory = sorted(set(packages))
    if not inventory:
        raise ValueError("empty Go package inventory")
    prefix = module + "/"
    targets = []
    for index, package in enumerate(inventory):
        if package != module and not package.startswith(prefix):
            raise ValueError("package outside the root module: " + package)
        if index % shards == shard - 1:
            targets.append("." if package == module else "./" + package[len(prefix):])
    return targets


def lint_packages(command, targets, run=subprocess.run):
    failed = []
    for target in targets:
        print("Checking " + target, flush=True)
        result = run([*command, "run", "--timeout=20m", target], check=False)
        if result.returncode:
            failed.append(target)
    if failed:
        print("Lint failed for: " + ", ".join(failed), file=sys.stderr)
        return 1
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--shard", type=int, required=True)
    parser.add_argument("--shards", type=int, required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        parser.error("provide the linter command after --")
    go = os.environ.get("GO", "go")
    try:
        module = subprocess.check_output([go, "list", "-m", "-f", "{{.Path}}"], text=True).strip()
        inventory = subprocess.check_output([go, "list", "./..."], text=True).splitlines()
        targets = select_packages(inventory, module, args.shard, args.shards)
    except (subprocess.CalledProcessError, ValueError) as error:
        print("Cannot enumerate lint packages: " + str(error), file=sys.stderr)
        return 1
    print(f"Lint shard {args.shard}/{args.shards}: {len(targets)}/{len(set(inventory))} packages", flush=True)
    return lint_packages(command, targets)


if __name__ == "__main__":
    sys.exit(main())
