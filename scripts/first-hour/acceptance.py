#!/usr/bin/env python3
"""Run only the opt-in first-hour journey through the existing journey runner.

An explicit target and authentication choice are required. Holds one new app,
submits four releases, performs no broad sweep, and always attempts cleanup.
"""
import argparse
import hashlib
import json
import os
import subprocess
import time
from pathlib import Path
from urllib.parse import urlsplit

from baseline import read_jsonl, scorecard, markdown

ROOT = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api", required=True)
    parser.add_argument("--cli", type=Path, required=True)
    auth = parser.add_mutually_exclusive_group(required=True)
    auth.add_argument("--cli-login", action="store_true", help="use the explicitly authorized existing CLI login")
    auth.add_argument("--token-file", type=Path)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--agents", type=Path, help="existing first-hour agent-eval JSONL; does not start agents")
    parser.add_argument("--plan", default="unknown")
    args = parser.parse_args()
    origin = urlsplit(args.api)
    if origin.scheme not in ("http", "https") or not origin.hostname or origin.username or origin.password or origin.query or origin.fragment or origin.path not in ("", "/"):
        parser.error("--api must be an HTTP(S) origin without credentials, path or query")
    cli, out = args.cli.resolve(), args.out.resolve()
    if not cli.is_file() or not os.access(cli, os.X_OK):
        parser.error("--cli must name an executable CLI")
    env = {k: v for k, v in os.environ.items() if not k.startswith("GREGALE_E2E_") and k != "FAAS_TOKEN"}
    env["GREGALE_E2E_FIRST_HOUR"] = "1"
    out.mkdir(parents=True, exist_ok=True)
    if any((out / name).exists() for name in ("go-test.json", "metadata.json", "baseline.json")):
        parser.error("--out already contains evidence; choose a fresh run directory")
    # Per-command development flags match the journey package's stdlib-only
    # dependency graph; do not change the machine's persistent Go environment.
    goflags = subprocess.run(["go", "env", "GOFLAGS"], capture_output=True, text=True, check=True).stdout.strip()
    env["GOFLAGS"] = goflags + " -trimpath -gcflags=all=-dwarf=false"
    version = subprocess.run([str(cli), "version"], capture_output=True, text=True, check=True).stdout.strip()
    revision = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, capture_output=True, text=True, check=True).stdout.strip()
    dirty = bool(subprocess.run(["git", "status", "--porcelain"], cwd=ROOT, capture_output=True, text=True, check=True).stdout.strip())
    with cli.open("rb") as binary:
        checksum = hashlib.sha256()
        for chunk in iter(lambda: binary.read(1024 * 1024), b""):
            checksum.update(chunk)
        digest = checksum.hexdigest()
    metadata = {"target": args.api, "cli_version": version, "harness_revision": revision, "cli_sha256": digest,
                "harness_worktree_dirty": dirty, "plan": args.plan, "evidence_kind": "deployed", "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    supported = subprocess.run([str(cli), "release", "verify", "--help"], env=env, capture_output=True, text=True, timeout=30).returncode == 0
    metadata["release_verify_available"] = supported
    (out / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    command = ["bash", str(ROOT / "scripts/e2e-journeys.sh"), "--api", args.api, "--cli", str(cli),
               "--tier", "full", "--run", "^TestFirstHourCLI$", "--max-apps", "1", "--max-deploys", "1",
               "--sweep-hours", "0", "--out", str(out)]
    if args.token_file:
        command += ["--token-file", str(args.token_file.resolve())]
    result = subprocess.run(command, cwd=ROOT, env=env, check=False) if supported else subprocess.CompletedProcess(command, 2)
    journey = out / "go-test.json"
    report = scorecard(read_jsonl(journey if journey.exists() else None, tolerant=True), read_jsonl(args.agents), metadata)
    (out / "baseline.json").write_text(json.dumps(report, indent=2, allow_nan=False) + "\n")
    (out / "baseline.md").write_text(markdown(report))
    print(f"Baseline: {out / 'baseline.md'}")
    return result.returncode or int(report["coverage"]["scripted_cli"] != "passed")


if __name__ == "__main__":
    raise SystemExit(main())
