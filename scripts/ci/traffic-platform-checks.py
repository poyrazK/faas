#!/usr/bin/env python3
"""ADR-570: retain complete traffic test logs and reject skipped PG acceptance."""

import argparse
import collections
import datetime
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import time


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def source_snapshot(repo):
    paths = subprocess.check_output(["git", "ls-files", "-z"], cwd=repo).decode().split("\0")
    return {
        "commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip(),
        "files": {path: hashlib.sha256((repo / path).read_bytes()).hexdigest()
                  for path in sorted(paths) if path and (repo / path).is_file()},
    }


def require_space(path, minimum):
    free = shutil.disk_usage(path).free
    if free < minimum:
        raise RuntimeError(f"build storage unavailable: {free} bytes free, {minimum} required")


def test_args(scope, phase, package):
    flags = ["go", "test", "-json", "-count=1", "-p=1", "-vet=off",
             "-gcflags=all=-l -dwarf=false", "-ldflags=-s -w"]
    return flags + [package] + (["-run", scope["postgres_selector"]] if phase == "postgres" else [])


def summarize_log(path, expected_packages, required_tests=()):
    packages, tests, failures = {}, {}, []
    with path.open() as stream:
        for line in stream:
            event = json.loads(line)
            action, package, test = event.get("Action"), event.get("Package"), event.get("Test")
            if action in ("fail", "build-fail"):
                failures.append(f"{package}:{test or '<package>'}")
            if action in ("pass", "skip", "fail"):
                if test:
                    tests[(package, test)] = action
                else:
                    packages[package] = action
    if failures:
        raise ValueError(f"failed test/package events: {failures}")
    if set(packages) != set(expected_packages) or any(value != "pass" for value in packages.values()):
        raise ValueError(f"incomplete package terminals: {packages}")
    if not tests or not any(action == "pass" for action in tests.values()):
        raise ValueError("no named test executed")
    if required_tests:
        missing = sorted(key for key in required_tests if tests.get(key) != "pass")
        skipped = sorted(key for key, action in tests.items() if action == "skip")
        if missing or skipped:
            raise ValueError(f"PostgreSQL acceptance missing/skipped: missing={missing}, skipped={skipped}")
    descendants = collections.defaultdict(list)
    for key in tests:
        parts = key[1].split("/")
        for depth in range(1, len(parts)):
            parent = (key[0], "/".join(parts[:depth]))
            if parent in tests:
                descendants[parent].append(key)
    guarded_parents = set()
    for key in sorted(tests, key=lambda item: item[1].count("/"), reverse=True):
        children = descendants[key]
        if tests[key] == "pass" and children and all(tests[child] == "skip" or child in guarded_parents for child in children):
            guarded_parents.add(key)
    passed = {key for key, action in tests.items() if action == "pass" and key not in guarded_parents}
    guarded = set(tests) - passed
    fixture_names = {test for _, test in required_tests}
    return {
        "packages": sorted(packages),
        "accepted_named_passes": len(passed),
        "guarded_or_skipped": len(guarded),
        "guarded_parent_passes": sorted(guarded_parents),
        "postgres_fixture_named_results": sum(test.split("/")[0] in fixture_names for _, test in passed),
        "required_postgres_tests": sorted(required_tests),
        "accepted_tests": sorted(passed),
        "guarded_tests": sorted(guarded),
    }


def run_phase(repo, output, scope, phase):
    require_space(output, 10 * 1024**3)
    receipt_path = output / f"{phase}.json"
    if receipt_path.exists():
        raise ValueError(f"refusing to overwrite terminal evidence: {receipt_path}")
    snapshot = source_snapshot(repo)
    expected_sha = os.environ.get("GITHUB_SHA")
    if expected_sha and snapshot["commit"] != expected_sha:
        raise ValueError("checkout does not match dispatched source commit")
    freeze_path = output / "source-freeze.json"
    if freeze_path.exists():
        if json.loads(freeze_path.read_text()) != snapshot:
            raise ValueError("source changed between traffic phases")
    else:
        write_json(freeze_path, snapshot)
    env = os.environ.copy()
    if phase == "unit":
        for key in ("DATABASE_URL", "FAAS_PGTEST_DATABASE_URL", "FAAS_PGTEST_TEMPLATE_DATABASE", "FAAS_SKIP_PG_TESTS"):
            env.pop(key, None)
    elif not env.get("DATABASE_URL") or env.get("FAAS_SKIP_PG_TESTS"):
        raise ValueError("PostgreSQL phase requires DATABASE_URL and forbids FAAS_SKIP_PG_TESTS")
    started = time.monotonic()
    commands = []
    with (output / f"{phase}.jsonl").open("wb") as stdout, (output / f"{phase}.stderr").open("wb") as stderr:
        for package in scope["packages"]:
            require_space(output, 2 * 1024**3)
            args = test_args(scope, phase, package)
            print(json.dumps({"phase": phase, "package": package}), flush=True)
            at = time.monotonic()
            child = subprocess.run(args, cwd=repo, env=env, stdout=stdout, stderr=stderr)
            commands.append({"args": args, "exit_code": child.returncode, "seconds": round(time.monotonic() - at, 3)})
    receipt = {"phase": phase, "commit": snapshot["commit"], "seconds": round(time.monotonic() - started, 3),
               "execution": "serial_package_commands", "commands": commands,
               "exit_code": int(any(command["exit_code"] for command in commands)),
               "runtime_environment": {key: env.get(key) for key in ("CGO_ENABLED", "GOMAXPROCS", "GOGC", "GOTMPDIR", "GOCACHE")},
               "source_unchanged": source_snapshot(repo) == snapshot}
    write_json(receipt_path, receipt)
    if receipt["exit_code"] or not receipt["source_unchanged"]:
        raise ValueError(f"traffic {phase} phase failed; see retained logs/receipt")


def verify(repo, output, scope):
    freeze = json.loads((output / "source-freeze.json").read_text())
    if source_snapshot(repo) != freeze:
        raise ValueError("source changed after tests")
    module = (repo / "go.mod").read_text().splitlines()[0].split()[1]
    expected = [module + package[1:] for package in scope["packages"]]
    required = {(item["package"], item["test"]) for item in scope["required_postgres_tests"]}
    if not required:
        raise ValueError("PostgreSQL acceptance requirements cannot be empty")
    runs = {}
    for phase in ("unit", "postgres"):
        receipt = json.loads((output / f"{phase}.json").read_text())
        if receipt["commit"] != freeze["commit"] or receipt["exit_code"] or not receipt["source_unchanged"]:
            raise ValueError(f"invalid {phase} terminal receipt")
        if len(receipt["commands"]) != len(scope["packages"]) or any(command["exit_code"] for command in receipt["commands"]):
            raise ValueError(f"incomplete {phase} commands")
        if [command["args"] for command in receipt["commands"]] != [test_args(scope, phase, package) for package in scope["packages"]]:
            raise ValueError(f"changed {phase} test scope")
        summary = summarize_log(output / f"{phase}.jsonl", expected, required if phase == "postgres" else ())
        runs[phase] = {"seconds": receipt["seconds"], **summary}
    accepted = {tuple(key) for run in runs.values() for key in run["accepted_tests"]}
    guarded = {tuple(key) for run in runs.values() for key in run["guarded_tests"]}
    result = {"at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "commit": freeze["commit"],
              "source_files": len(freeze["files"]), "source_unchanged": True, "runs": runs,
              "distinct_named_passes": len(accepted), "guarded_without_acceptance": len(guarded - accepted),
              "native_executions": 0, "acceptance": "Software tests only; native/deployed/release qualification remains open."}
    write_json(output / "results.json", result)
    lines = [f"## Traffic software checks — `{freeze['commit']}`", "",
             "| Phase | Named passes | Guarded/skipped | PostgreSQL fixture results |",
             "| --- | ---: | ---: | ---: |"]
    for phase, run in runs.items():
        lines.append(f"| {phase} | {run['accepted_named_passes']} | {run['guarded_or_skipped']} | {run['postgres_fixture_named_results']} |")
    lines += ["", result["acceptance"], ""]
    summary = "\n".join(lines)
    (output / "summary.md").write_text(summary)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as stream:
            stream.write(summary)
    print(summary)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("phase", choices=("unit", "postgres", "verify"))
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    repo = pathlib.Path(__file__).resolve().parents[2]
    scope = json.loads((repo / "scripts/ci/traffic-platform-scope.json").read_text())
    args.output.mkdir(parents=True, exist_ok=True)
    if args.phase == "verify":
        verify(repo, args.output, scope)
    else:
        run_phase(repo, args.output, scope, args.phase)


if __name__ == "__main__":
    main()
