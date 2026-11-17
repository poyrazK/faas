#!/usr/bin/env python3
"""Qualify one published CLI archive with the existing first-hour journey.

Downloads are temporary; the installed CLI and login are never changed. Only
qualification.json/md and baseline.json/md are suitable for CI artifacts.
"""
import argparse
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
from pathlib import Path
from urllib.error import URLError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

from baseline import markdown, read_jsonl, scorecard

ROOT = Path(__file__).resolve().parents[2]
REPO = "poyrazK/faas"
TAG = re.compile(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?")
SHA = re.compile(r"[0-9a-f]{40}")
MAX_ARCHIVE = 128 * 1024 * 1024
MAX_BINARY = 256 * 1024 * 1024


class QualificationError(Exception):
    def __init__(self, code, status="failed"):
        self.code, self.status = code, status
        super().__init__(code)


def download(url, destination, limit):
    request = Request(url, headers={"User-Agent": "gregale-first-hour-qualification"})
    try:
        with urlopen(request, timeout=60) as response, destination.open("xb") as output:
            size = 0
            while chunk := response.read(1024 * 1024):
                size += len(chunk)
                if size > limit:
                    raise QualificationError("download_too_large")
                output.write(chunk)
    except (URLError, TimeoutError) as error:
        raise QualificationError("download_failed") from error


def github_json(resource, directory, name):
    path = directory / name
    download(f"https://api.github.com/repos/{REPO}/{resource}", path, 2 * 1024 * 1024)
    return json.loads(path.read_text())


def source_commit(tag, directory):
    ref = github_json(f"git/ref/tags/{tag}", directory, "tag-ref.json")["object"]
    # Both lightweight and annotated release tags are supported.
    for depth in range(4):
        if ref.get("type") == "commit" and SHA.fullmatch(ref.get("sha", "")):
            return ref["sha"]
        if ref.get("type") != "tag" or not SHA.fullmatch(ref.get("sha", "")):
            break
        ref = github_json(f"git/tags/{ref['sha']}", directory, f"tag-{depth}.json")["object"]
    raise QualificationError("invalid_release_source")


def checksum_for(text, archive):
    matches = []
    for line in text.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[1].lstrip("*") == archive:
            if not re.fullmatch(r"[0-9a-fA-F]{64}", fields[0]):
                raise QualificationError("invalid_checksum_manifest")
            matches.append(fields[0].lower())
    if len(matches) != 1:
        raise QualificationError("invalid_checksum_manifest")
    return matches[0]


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def unpack(archive, directory):
    with tarfile.open(archive, "r:gz") as bundle:
        members = bundle.getmembers()
        if len(members) != 1 or members[0].name != "gregale" or not members[0].isfile() or not 0 < members[0].size <= MAX_BINARY:
            raise QualificationError("unsafe_cli_archive")
        binary = directory / "gregale"
        # Copy the single regular member instead of extracting paths/links.
        with bundle.extractfile(members[0]) as source, binary.open("xb") as output:
            shutil.copyfileobj(source, output)
        binary.chmod(0o700)
        return binary


def host_target():
    system = {"Darwin": "darwin", "Linux": "linux"}.get(platform.system())
    arch = {"x86_64": "amd64", "amd64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine())
    if not system or not arch:
        raise QualificationError("unsupported_host", "blocked")
    return system, arch


def prepare_cli(tag, directory, evidence):
    if not TAG.fullmatch(tag):
        raise QualificationError("invalid_release_tag")
    evidence.update(release_tag=tag, release_url=f"https://github.com/{REPO}/releases/tag/{tag}")
    release = github_json(f"releases/tags/{tag}", directory, "release.json")
    if release.get("tag_name") != tag or release.get("draft") or not release.get("published_at"):
        raise QualificationError("release_not_published", "blocked")
    system, arch = host_target()
    name = f"gregale_{tag[1:]}_{system}_{arch}.tar.gz"
    base = f"https://github.com/{REPO}/releases/download/{tag}"
    assets = {asset["name"] for asset in release.get("assets", []) if asset.get("state") == "uploaded"}
    evidence.update(published_at=release["published_at"], platform=f"{system}/{arch}", archive=name, archive_url=f"{base}/{name}")
    if not {name, "CLI-SHA256SUMS"} <= assets:
        raise QualificationError("release_assets_missing", "blocked")
    manifest, archive = directory / "CLI-SHA256SUMS", directory / name
    download(f"{base}/CLI-SHA256SUMS", manifest, 1024 * 1024)
    expected = checksum_for(manifest.read_text(), name)
    evidence.update(checksum_manifest_sha256=digest(manifest), expected_archive_sha256=expected)
    download(f"{base}/{name}", archive, MAX_ARCHIVE)
    evidence["archive_sha256"] = digest(archive)
    if evidence["archive_sha256"] != expected:
        raise QualificationError("archive_checksum_mismatch")
    evidence["checksum_verified"] = True
    cli = unpack(archive, directory)
    evidence["cli_sha256"] = digest(cli)
    env = {key: value for key, value in os.environ.items() if key not in ("FAAS_TOKEN", "FAAS_JSON") and not key.startswith("GREGALE_E2E_")}
    version = subprocess.run([str(cli), "version", "--json"], env=env, capture_output=True, text=True, timeout=30, check=True)
    identity = json.loads(version.stdout)
    if identity.get("version") != tag:
        raise QualificationError("cli_version_mismatch")
    commit = source_commit(tag, directory)
    if identity.get("git_sha") != commit:
        raise QualificationError("cli_source_mismatch")
    evidence.update(cli_version=tag, cli_source_sha=commit)
    supported = subprocess.run([str(cli), "release", "verify", "--help"], env=env, capture_output=True, timeout=30).returncode == 0
    evidence["release_verify_available"] = supported
    if not supported:
        raise QualificationError("release_verify_unavailable", "blocked")
    evidence["status"] = "passed"
    return cli


def api_origin(value):
    parsed = urlsplit(value or "")
    if parsed.scheme not in ("https", "http") or not parsed.hostname or parsed.username or parsed.password or parsed.path not in ("", "/") or parsed.query or parsed.fragment:
        raise QualificationError("invalid_or_missing_api", "blocked")
    return value.rstrip("/")


def run_journey(args, cli, artifact, report):
    api = api_origin(args.api)
    report["target"] = api
    if not args.cli_login and not args.token_file:
        raise QualificationError("authentication_not_configured", "blocked")
    if args.token_file and (not args.token_file.is_file() or not args.token_file.read_text().strip()):
        raise QualificationError("authentication_not_configured", "blocked")
    private = args.out / "private"
    private.mkdir(mode=0o700)
    journey = private / "journey"
    command = [sys.executable, str(ROOT / "scripts/first-hour/acceptance.py"), "--api", api,
               "--cli", str(cli), "--plan", args.plan, "--out", str(journey)]
    command += ["--cli-login"] if args.cli_login else ["--token-file", str(args.token_file.resolve())]
    log_path = private / "journey.log"
    with log_path.open("x") as log:
        log_path.chmod(0o600)
        result = subprocess.run(command, cwd=ROOT, stdout=log, stderr=subprocess.STDOUT, check=False)
    if not (journey / "metadata.json").is_file():
        raise QualificationError("journey_evidence_missing")
    metadata = json.loads((journey / "metadata.json").read_text())
    if metadata.get("cli_sha256") != artifact["cli_sha256"] or metadata.get("target", "").rstrip("/") != api:
        raise QualificationError("journey_provenance_mismatch")
    baseline = scorecard(read_jsonl(journey / "go-test.json" if (journey / "go-test.json").exists() else None, tolerant=True), [], metadata)
    (args.out / "baseline.json").write_text(json.dumps(baseline, indent=2, allow_nan=False) + "\n")
    (args.out / "baseline.md").write_text(markdown(baseline))
    report["journey"] = {"status": baseline["coverage"]["scripted_cli"], "steps": baseline["steps"], "metrics": baseline["metrics"],
                         "harness_revision": baseline["provenance"]["harness_revision"],
                         "harness_worktree_dirty": baseline["provenance"]["harness_worktree_dirty"]}
    if result.returncode or report["journey"]["status"] != "passed":
        raise QualificationError("journey_failed_or_incomplete")


def render(report):
    lines = ["# Released CLI first-hour qualification", "", f"Outcome: **{report['status']}**.", "",
             f"Reason: `{report['reason']}`.", "", "| Artifact | Evidence |", "|---|---|"]
    lines.extend(f"| {key} | {str(value).replace('|', '/').replace(chr(10), ' ')} |" for key, value in report["artifact"].items())
    lines += ["", f"Target: {report.get('target') or 'not configured'}.",
              f"Deployed journey: **{report['journey']['status']}**.", "",
              "This qualifies only the recorded platform archive and authenticated scripted journey.",
              "Fresh installation/login, installer channel selection, interactive Start, other platforms/plans and coding agents remain unmeasured.",
              "CLI qualification does not certify the other release-pipeline jobs.",
              "Raw journey logs and credentials are excluded from these reports."]
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release", required=True, help="exact published vX.Y.Z[-prerelease] tag")
    parser.add_argument("--api", help="explicit acceptance API origin; absence is reported as blocked")
    auth = parser.add_mutually_exclusive_group()
    auth.add_argument("--cli-login", action="store_true")
    auth.add_argument("--token-file", type=Path)
    parser.add_argument("--plan", default="unknown")
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    args.out = args.out.resolve()
    if args.out.exists():
        parser.error("--out must be a fresh directory")
    args.out.mkdir(parents=True, mode=0o700)
    report = {"schema_version": 1, "status": "failed", "reason": "qualification_interrupted",
              "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "artifact": {"status": "not_measured"}, "journey": {"status": "not_measured"}}
    phase = "artifact"
    try:
        with tempfile.TemporaryDirectory(prefix="gregale-release-qualification-") as temporary:
            cli = prepare_cli(args.release, Path(temporary), report["artifact"])
            phase = "journey"
            run_journey(args, cli, report["artifact"], report)
            report.update(status="passed", reason="released_artifact_and_journey_passed")
    except QualificationError as error:
        report.update(status=error.status, reason=error.code)
        report[phase]["status"] = error.status
    except (OSError, ValueError, KeyError, tarfile.TarError, subprocess.SubprocessError):
        # Exceptions can contain paths, private API output or credentials.
        report.update(status="failed", reason="qualification_tool_error")
        report[phase]["status"] = "failed"
    finally:
        (args.out / "qualification.json").write_text(json.dumps(report, indent=2, allow_nan=False) + "\n")
        (args.out / "qualification.md").write_text(render(report))
    print(f"Released CLI qualification: {report['status']} ({report['reason']})")
    print(f"Report: {args.out / 'qualification.md'}")
    return int(report["status"] != "passed")


if __name__ == "__main__":
    raise SystemExit(main())
