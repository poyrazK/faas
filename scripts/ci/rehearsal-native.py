#!/usr/bin/env python3
"""ADR-20261018000100: native running-import journey; never enables rollout gates."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import stat
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid

FIXTURE = "tests/scenario-acceptance/rehearsal-running-job"
FILES = ("package.json", "package-lock.json", "server.mjs", "commands.mjs", "input.json",
         "fixture.sql", "gregale-test.yaml", "worker/package.json", "worker/package-lock.json",
         "worker/server.mjs", "worker/importer.mjs", "worker/revision.json")
SHA = re.compile(r"[0-9a-f]{40}\Z")
NATIVE_LOCK = Path("/var/lock/faas-builder-acceptance.lock")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def write_json(path, value):
    # Journals remain reviewable after interruption, without truncating the old entry.
    pending = path.with_suffix(".pending")
    pending.write_text(json.dumps(value, indent=2) + "\n")
    os.replace(pending, path)


def file_digest(path):
    result = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            result.update(chunk)
    return result.hexdigest()


def git(repo, *args):
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    env.update(GIT_TERMINAL_PROMPT="0", GIT_NO_LAZY_FETCH="1")
    return subprocess.check_output(["git", "--no-replace-objects", "-c", "core.hooksPath=/dev/null",
                                    "-c", "commit.gpgsign=false", "-C", str(repo), *args],
                                   env=env, text=True, stderr=subprocess.DEVNULL).strip()


def prepare_fixture(repo, destination, journey="running-job"):
    require(journey in ("running-job", "postgres-recovery"), "unsupported qualification journey")
    destination.mkdir(mode=0o700)
    for name in FILES:
        source = repo / FIXTURE / name
        if journey == "postgres-recovery" and name == "gregale-test.yaml":
            source = repo / FIXTURE / "gregale-postgres-fault-test.yaml"
        require(source.is_file() and not source.is_symlink(), "missing regular fixture input: " + name)
        target = destination / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(source.read_bytes())
    require(json.loads((destination / "worker/revision.json").read_text()) == {"duplicate_notification": False},
            "fixture baseline must suppress duplicate notifications")
    git(destination, "init", "--quiet")
    git(destination, "config", "user.email", "rehearsal-acceptance@example.test")
    git(destination, "config", "user.name", "Gregale rehearsal acceptance")
    git(destination, "add", "--force", "--", *FILES)
    git(destination, "commit", "--quiet", "-m", "Pinned idempotent import")
    baseline = git(destination, "rev-parse", "HEAD")
    (destination / "worker/revision.json").write_text('{"duplicate_notification":true}\n')
    git(destination, "add", "--", "worker/revision.json")
    git(destination, "commit", "--quiet", "-m", "Duplicate notification on managed retry")
    candidate = git(destination, "rev-parse", "HEAD")
    require(git(destination, "diff", "--name-only", baseline, candidate) == "worker/revision.json",
            "candidate may change only the notification retry behavior")
    return {"baseline": baseline, "candidate": candidate}


def root_file(path):
    metadata = path.lstat()
    require(stat.S_ISREG(metadata.st_mode) and metadata.st_uid == 0 and not metadata.st_mode & 0o022,
            "native host marker and release stamp must be root-owned regular files without group/other write access")
    return path.read_text().strip()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def api_get(origin, path):
    request = urllib.request.Request(origin + path, headers={"Authorization": "Bearer " + os.environ["FAAS_TOKEN"]})
    try:
        with urllib.request.build_opener(NoRedirect()).open(request, timeout=15) as response:
            body = response.read(1024 * 1024 + 1)
            require(len(body) <= 1024 * 1024, "API response exceeded its evidence bound")
            return response.status, json.loads(body)
    except urllib.error.HTTPError as error:
        return error.code, None  # Do not print tokens, API bodies or fixture credentials.


def runner_configuration(repo):
    require(os.environ.get("GREGALE_REHEARSAL_NATIVE_ACCEPTANCE") == "1", "set GREGALE_REHEARSAL_NATIVE_ACCEPTANCE=1 on the dedicated runner")
    require(platform.system() == "Linux" and platform.machine() == "x86_64" and os.geteuid() == 0,
            "requires root on dedicated native x86_64 Linux")
    require(Path("/dev/kvm").is_char_device() and os.access("/dev/kvm", os.R_OK | os.W_OK), "requires accessible native KVM")
    require(os.environ.get("FAAS_TEST_SKIP_POSTGRES") != "1" and os.environ.get("FAAS_TEST_FAKE_STORAGE") != "1",
            "native qualification refuses PostgreSQL skips and storage emulation")
    marker = json.loads(root_file(Path("/etc/faas/rehearsal-acceptance-host.json")))
    installed = root_file(Path("/etc/faas/rehearsal-acceptance-platform-sha"))
    require(SHA.fullmatch(installed) and installed == git(repo, "rev-parse", "HEAD"), "installed platform release must match this checkout")
    require(not git(repo, "status", "--porcelain"), "native qualification requires a clean checkout")
    origin = os.environ.get("FAAS_API", "")
    url = urllib.parse.urlsplit(origin)
    require(url.scheme == "https" and url.hostname and not url.username and not url.password and
            url.path in ("", "/") and not url.query and not url.fragment, "FAAS_API must be a dedicated HTTPS API origin")
    origin = origin.rstrip("/")
    account = marker.get("account_id", "")
    require(str(uuid.UUID(account)) == account and marker.get("dedicated_native_host") is True and
            marker.get("api_origin") == origin, "root-owned host marker must bind the native host, API and dedicated account")
    require(bool(os.environ.get("FAAS_TOKEN")), "missing dedicated acceptance account FAAS_TOKEN")
    status, who = api_get(origin, "/v1/account")
    require(status == 200 and isinstance(who, dict) and who.get("id") == account, "token does not identify the dedicated acceptance account")
    for tool in ("go", "git", "make", "ip", "nft", "firecracker", "jailer", "busybox", "mkfs.ext4", "losetup", "findmnt"):
        require(shutil.which(tool), "missing native acceptance tool: " + tool)
    return {"platform_commit": installed, "api_origin": origin, "account_id": account, "native_runner": True}


def execute(argv, repo, log, timeout, expected=0):
    # Only this process group is interrupted; let the CLI run its owner cleanup.
    with log.open("xb") as output:
        child = subprocess.Popen([str(arg) for arg in argv], cwd=repo, stdout=output,
                                 stderr=subprocess.STDOUT, start_new_session=True)
        try:
            code = child.wait(timeout=timeout)
        finally:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGINT)
                try:
                    child.wait(timeout=180)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait()
    require(code == expected, "native command failed; retained log: " + str(log))


def verify_app_cleanup(origin, paths):
    slugs = []
    for path in paths:
        require(path.stat().st_size <= 16 * 1024 * 1024, "oversized report")
        report = json.loads(path.read_text())
        for run in (*report["baseline_runs"], *report["candidate_runs"]):
            slugs.extend((run["app_slug"], *run["services"].values()))
    require(len(slugs) == 8 and len(set(slugs)) == 8, "expected eight isolated workloads")
    for slug in slugs:
        status, _ = api_get(origin, "/v1/apps/" + urllib.parse.quote(slug, safe=""))
        require(status == 404, "isolated workload deletion was not independently confirmed")
    return slugs


def native_run(repo, out, journey="running-job"):
    identity = runner_configuration(repo)
    journal = {"version": 1, "status": "failed", "scope": journey + "-journey", **identity}
    write_json(out / "qualification.json", journal)
    # Share the native builder acceptance lock; do not stop any existing fleet.
    with NATIVE_LOCK.open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            execute(["make", "leakcheck"], repo, out / "leak-before.log", 60)
            execute(["go", "build", "-mod=readonly", "-buildvcs=true", "-o", out / "gregale", "./cmd/gregale"], repo, out / "build-cli.log", 600)
            execute(["go", "build", "-mod=readonly", "-buildvcs=true", "-o", out / "verify", "./scripts/ci/rehearsalacceptance"], repo, out / "build-verifier.log", 600)
            if journey == "running-job":
                # Exercise the real VMM pause and guest resume hook as a prerequisite.
                execute(["bash", "scripts/ci/rehearsal-suspend-resume-acceptance.sh"], repo, out / "native-pause.log", 600)
                require("--- PASS: TestMetalRehearsalSuspendResume " in (out / "native-pause.log").read_text(), "native pause prerequisite skipped or did not execute")
            pins = prepare_fixture(repo, out / "fixture", journey)
            journal["fixture"] = pins
            write_json(out / "qualification.json", journal)
            reports = []
            for case, candidate, code in (("control", pins["baseline"], 0), ("regression", pins["candidate"], 1)):
                print("Running native rehearsal " + case, flush=True)
                args = [out / "gregale", "test", "rehearse", "--repo", out / "fixture", "--base", pins["baseline"],
                        "--candidate", candidate, "--manifest", out / "fixture/gregale-test.yaml", "--scenario", "running-import",
                        "--engine", "real-vm", "--profile", "warm", "--repeat", "1", "--max-workload-minutes", "120"]
                execute([*args, "--validate"], repo, out / (case + "-validate.log"), 60)
                path = out / (case + ".json")
                execute([*args, "--report", path, "--markdown", out / (case + ".md")], repo, out / (case + ".log"), 1500, code)
                reports.append(path)
            execute([out / "verify", "--control", reports[0], "--regression", reports[1], "--baseline", pins["baseline"],
                     "--candidate", pins["candidate"], "--account", identity["account_id"], "--journey", journey], repo, out / "verification.json", 60)
            journal["deleted_workloads"] = verify_app_cleanup(identity["api_origin"], reports)
            require(runner_configuration(repo) == identity, "native platform identity changed during qualification")
            journal["journey_evidence"] = "passed"
        finally:
            # No reaping before observation: leaks fail and remain available for investigation.
            execute(["make", "leakcheck"], repo, out / "leak-after.log", 60)
        journal["artifacts"] = {p.name: file_digest(p) for p in out.iterdir()
                                if p.is_file() and p.name != "qualification.json"}
        journal["status"] = "passed"
        write_json(out / "qualification.json", journal)
    print("Native " + journey + " journey passed. Evidence: " + str(out), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--out", type=Path)
    parser.add_argument("--check-runner", action="store_true")
    parser.add_argument("--prepare-only", action="store_true", help="offline fixture preparation; never qualifies native support")
    parser.add_argument("--journey", choices=("running-job", "postgres-recovery"), default="running-job")
    args = parser.parse_args()
    repo = args.repo.resolve()
    os.umask(0o077)
    # Build the stamped source rather than an ambient workspace or Go overlay.
    os.environ["GOWORK"], os.environ["GOFLAGS"] = "off", ""
    if args.check_runner:
        runner_configuration(repo)
        return
    require(args.out is not None, "--out must name a new private evidence directory")
    out = args.out.absolute()
    out.mkdir(mode=0o700)  # Exclusive: do not overwrite or adopt an old run.
    if args.prepare_only:
        pins = prepare_fixture(repo, out / "fixture", args.journey)
        write_json(out / "preparation.json", {"version": 1, "status": "prepared", "journey": args.journey, "native_qualified": False, **pins})
        return
    native_run(repo, out, args.journey)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(1))
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError, KeyboardInterrupt) as error:
        print("Native rehearsal acceptance did not pass: " + str(error) + "; retain the evidence directory and its logs.", file=sys.stderr)
        sys.exit(1)
