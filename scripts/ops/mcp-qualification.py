#!/usr/bin/env python3
"""Prepare and validate redacted MCP release qualification evidence."""

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import tempfile


CHECKER_PATH = Path(__file__).resolve().parents[1] / "ci" / "mcp-qualification-check.py"
SPEC = importlib.util.spec_from_file_location("mcp_qualification_checker", CHECKER_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"could not load qualification checker at {CHECKER_PATH}")
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


def load_manifest(directory):
    root = directory.resolve()
    manifest_path = root / "manifest.json"
    if not manifest_path.is_file() or manifest_path.is_symlink():
        raise ValueError(f"{manifest_path} must be a regular manifest file")
    evidence = json.loads(manifest_path.read_text(encoding="utf-8"))
    if not isinstance(evidence, dict):
        raise ValueError("manifest must be a JSON object")
    if evidence.get("version") != 1:
        raise ValueError("manifest version must be 1")
    if not re.fullmatch(r"[0-9a-f]{40}", evidence.get("commit", "")):
        raise ValueError("manifest must identify a full lowercase 40-character commit SHA")
    records = evidence.get("checks")
    if not isinstance(records, list):
        raise ValueError("manifest checks must be a list")
    by_name = {record.get("name"): record for record in records if isinstance(record, dict)}
    if len(by_name) != len(records) or set(by_name) != CHECKER.REQUIRED:
        raise ValueError("manifest must contain exactly one record for every required qualification")
    return root, manifest_path, evidence, by_name


def atomic_write(path, evidence):
    body = json.dumps(evidence, indent=2, sort_keys=True) + "\n"
    temp_path = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="w", encoding="utf-8", dir=path.parent, prefix=".manifest-", delete=False
        ) as temp:
            temp_path = Path(temp.name)
            os.chmod(temp_path, 0o600)
            temp.write(body)
            temp.flush()
            os.fsync(temp.fileno())
        os.replace(temp_path, path)
    finally:
        if temp_path is not None and temp_path.exists():
            temp_path.unlink()


def init(args):
    if not re.fullmatch(r"[0-9a-f]{40}", args.commit):
        raise ValueError("--commit must be a full lowercase 40-character commit SHA")
    root = args.directory
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    manifest = root / "manifest.json"
    if manifest.exists() or manifest.is_symlink():
        raise ValueError(f"{manifest} already exists")
    evidence = {
        "version": 1,
        "commit": args.commit,
        "checks": [
            {"name": name, "status": "pending", "target": ""}
            for name in sorted(CHECKER.REQUIRED)
        ],
    }
    atomic_write(manifest, evidence)
    print(json.dumps({"ok": True, "manifest": str(manifest), "commit": args.commit, "checks": len(evidence["checks"])}))


def artifact_path(root, relative):
    path = Path(relative)
    if path.is_absolute() or not path.parts or ".." in path.parts:
        raise ValueError("artifact path must be a nonempty relative path inside the evidence directory")
    candidate = root / path
    resolved = candidate.resolve(strict=True)
    if not resolved.is_relative_to(root) or not resolved.is_file():
        raise ValueError("artifact must be a regular file inside the evidence directory")
    current = root
    for part in path.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError("artifact paths cannot contain symlinks")
    return path, resolved


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_native_rollout_report(receipt, commit):
    try:
        report = json.loads(receipt.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return
    if not isinstance(report, dict) or report.get("scope") != "native-mcp-hosting-rollout":
        return
    if report.get("status") != "passed" or report.get("native_observations") is not True:
        raise ValueError("native rollout report did not pass with native observations")
    if report.get("reviewed_commit") != commit:
        raise ValueError("native rollout report commit does not match the evidence manifest")
    provenance = report.get("provenance")
    if not isinstance(provenance, dict) or provenance.get("source_revision") != commit or provenance.get("binary_revision") != commit or provenance.get("binary_modified") is not False:
        raise ValueError("native rollout report lacks matching clean source and binary provenance")
    for field in ("binary_sha256", "plan_sha256"):
        if not re.fullmatch(r"[0-9a-f]{64}", report.get(field, "")):
            raise ValueError(f"native rollout report lacks a valid {field}")
    if report.get("scenario") not in {"rollout", "interrupt", "bad-candidate", "stale-observer"}:
        raise ValueError("native rollout report has an unknown scenario")


def record(args):
    root, manifest, evidence, by_name = load_manifest(args.directory)
    if args.name not in CHECKER.REQUIRED:
        raise ValueError(f"unknown qualification name: {args.name}")
    if not args.target.strip():
        raise ValueError("--target must identify the test app, native host/build, or client version")
    relative, receipt = artifact_path(root, args.artifact)
    if args.status == "passed":
        validate_native_rollout_report(receipt, evidence["commit"])
    record = by_name[args.name]
    record.update({
        "status": args.status,
        "target": args.target.strip(),
        "artifact": relative.as_posix(),
        "sha256": sha256_file(receipt),
    })
    atomic_write(manifest, evidence)
    print(json.dumps({"ok": True, "name": args.name, "status": args.status, "artifact": relative.as_posix(), "sha256": record["sha256"]}))


def status(args):
    _, _, evidence, by_name = load_manifest(args.directory)
    rows = [
        {"name": name, "status": by_name[name].get("status", "pending"), "target": by_name[name].get("target", "")}
        for name in sorted(CHECKER.REQUIRED)
    ]
    complete = all(row["status"] == "passed" for row in rows)
    result = {"ok": False, "complete": complete, "commit": evidence["commit"], "checks": rows}
    if complete:
        try:
            result.update(CHECKER.check(args.directory.resolve() / "manifest.json", args.commit or evidence["commit"]))
        except (ValueError, KeyError, TypeError, OSError) as error:
            result["error"] = str(error)
    else:
        result["error"] = "qualification is incomplete; record every required check as passed"
    print(json.dumps(result))
    if not result.get("ok"):
        raise SystemExit(1)


def check(args):
    root, _, evidence, _ = load_manifest(args.directory)
    try:
        result = CHECKER.check(root / "manifest.json", args.commit or evidence["commit"])
    except (ValueError, KeyError, TypeError, OSError) as error:
        print(json.dumps({"ok": False, "error": str(error)}))
        raise SystemExit(1)
    print(json.dumps(result))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)

    init_parser = commands.add_parser("init", help="create an empty evidence manifest")
    init_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="private qualification evidence directory")
    init_parser.add_argument("--commit", required=True, help="full reviewed source commit SHA")
    init_parser.set_defaults(run=init)

    record_parser = commands.add_parser("record", help="hash a redacted receipt and update one evidence row")
    record_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="qualification evidence directory")
    record_parser.add_argument("--name", required=True, help="required qualification row name")
    record_parser.add_argument("--target", required=True, help="test app, native host/build, or client version")
    record_parser.add_argument("--artifact", required=True, help="redacted receipt path relative to --dir")
    record_parser.add_argument("--status", choices=("passed", "failed"), required=True)
    record_parser.set_defaults(run=record)

    for name, help_text, function in (
        ("status", "show recorded rows and evidence integrity", status),
        ("check", "run the fail-closed release evidence gate", check),
    ):
        command_parser = commands.add_parser(name, help=help_text)
        command_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="qualification evidence directory")
        command_parser.add_argument("--commit", help="expected reviewed source commit SHA (defaults to manifest)")
        command_parser.set_defaults(run=function)

    args = parser.parse_args()
    try:
        args.run(args)
    except (ValueError, KeyError, TypeError, OSError, json.JSONDecodeError) as error:
        print(json.dumps({"ok": False, "error": str(error)}))
        raise SystemExit(1)


if __name__ == "__main__":
    main()
