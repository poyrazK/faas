#!/usr/bin/env python3
"""Portable Gregale recovery validation; no KVM, deployment or credentials."""
import argparse
import datetime
import json
import hashlib
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = "./cmd/gregale"
FILTER = (
    "^(TestCampaign(Runner|Recovery|Bundle|Journal|Cancellation|Cleanup|Status)"
    "|TestRecovery(Assessment|Completion)|TestCleanupObservation|TestPortableJournal)"
)
REQUIRED = {
    "TestCampaignRecoveryAcceptanceCrashHandoffCleanupAndReconciliation",
    "TestCampaignRecoveryAcceptanceBlockingFailures",
    "TestCampaignRecoveryAcceptanceCompletedEvidenceCanPass",
    "TestCampaignRecoveryProcessInterruption",
    "TestCampaignRecoveryCoordinatorCrash",
    "TestCampaignRecoveryProcessIdentityGuards",
    "TestCampaignRecoveryClaimCrash",
    "TestCampaignRecoveryClaimGuards",
    "TestCampaignRecoveryRetryAfterProcessDeath",
    "TestCampaignRecoveryGuardProtocol",
    "TestCampaignRecoveryProcessReapedDuringStatRead",
}
GCFLAGS = "-gcflags=github.com/onebox-faas/faas/cmd/gregale=-dwarf=false -c=1"


def test_events(path):
    passed, failed, skipped = set(), set(), set()
    errors = []
    for number, line in enumerate(Path(path).read_text().splitlines(), 1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            errors.append("invalid test JSON on line %d" % number)
            continue
        if not isinstance(event, dict):
            errors.append("test JSON line %d is not an object" % number)
            continue
        name = event.get("Test")
        action = event.get("Action")
        if action == "fail":
            failed.add(name or event.get("Package", "<package>"))
        if name and action == "pass":
            passed.add(name)
        if name and action == "skip":
            skipped.add(name)
    missing = sorted(REQUIRED - passed)
    if missing:
        errors.append("required acceptance tests did not pass: " + ", ".join(missing))
    if skipped:
        errors.append("portable tests skipped: " + ", ".join(sorted(skipped)))
    if failed:
        errors.append("test failures: " + ", ".join(sorted(failed)))
    return {
        "passed": len(passed),
        "failed": sorted(failed),
        "skipped": sorted(skipped),
        "missing_acceptance_tests": missing,
        "errors": errors,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", help="new evidence directory (must not exist)")
    parser.add_argument("--format", action="store_true", help="explicitly gofmt CLI sources before checking")
    args = parser.parse_args()
    run_id = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + uuid.uuid4().hex[:8]
    output = Path(args.output or os.environ.get("RECOVERY_GATE_OUTPUT") or ROOT / ".cache" / "gregale-recovery" / run_id).resolve()
    if output.exists():
        parser.error("evidence directory already exists; choose a new --output")
    # Evidence must not replace source/configuration, even with --format.
    for protected in ("cmd", "pkg", "scripts", ".github", "docs"):
        if output == ROOT / protected or ROOT / protected in output.parents:
            parser.error("evidence directory must be outside source directories")
    output.mkdir(parents=True, mode=0o700)
    env = dict(os.environ)
    env.setdefault("GOMEMLIMIT", "6GiB")
    # Individual tests override these for their local HTTP fixtures.
    env["FAAS_API"] = "http://127.0.0.1:1"
    env["FAAS_TOKEN"] = "portable-gate-no-credentials"
    env.pop("GREGALE_INTERNAL_CAMPAIGN_JOURNAL", None)
    go = os.environ.get("GO", "go")
    stages = []
    summary = {"schema_version": 1, "run_id": run_id, "package": PACKAGE,
               "filter": FILTER, "required_acceptance_tests": sorted(REQUIRED),
               "format_sources_requested": args.format, "stages": stages, "passed": False}

    def save():
        (output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")

    def run(name, command, timeout=1800, stdout=None):
        started = time.monotonic()
        target = output / (stdout or name + ".log")
        stage = {"name": name, "argv": [str(x) for x in command], "stdout": target.name,
                 "stderr": name + ".stderr.log"}
        stages.append(stage)
        save()
        print("gregale-recovery: " + name, flush=True)
        try:
            with target.open("w") as out, (output / stage["stderr"]).open("w") as err:
                result = subprocess.run(stage["argv"], cwd=ROOT, env=env, stdout=out, stderr=err, timeout=timeout, check=False)
            stage["exit_code"] = result.returncode
        except subprocess.TimeoutExpired:
            stage["exit_code"] = 124
            stage["error"] = "stage timed out"
        except OSError as error:
            stage["exit_code"] = 127
            stage["error"] = str(error)
        stage["seconds"] = round(time.monotonic() - started, 3)
        stage["passed"] = stage["exit_code"] == 0
        save()
        return stage

    try:
        self_test = run("self-test", [sys.executable, "-m", "unittest", "discover", "-s", "scripts/ci", "-p", "gregale_recovery_gate_test.py"], timeout=60)
        if not self_test["passed"]:
            return 1
        toolchain = run("toolchain", [go, "env", "-json", "GOVERSION", "GOROOT", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH", "CGO_ENABLED"], stdout="toolchain.json")
        if not toolchain["passed"]:
            return 1
        metadata = json.loads((output / "toolchain.json").read_text())
        pinned = next(line.split()[1] for line in (ROOT / "go.mod").read_text().splitlines() if line.startswith("toolchain "))
        summary["toolchain"] = metadata
        if metadata["GOVERSION"] != pinned or metadata["CGO_ENABLED"] != "1" or (
            metadata["GOOS"], metadata["GOARCH"]) != (metadata["GOHOSTOS"], metadata["GOHOSTARCH"]):
            toolchain["passed"] = False
            toolchain["error"] = "requires pinned toolchain %s, native GOOS/GOARCH and CGO_ENABLED=1 for race checks" % pinned
            return 1
        files = sorted(str(path.relative_to(ROOT)) for path in (ROOT / "cmd" / "gregale").glob("*.go"))
        gofmt = str(Path(metadata["GOROOT"]) / "bin" / ("gofmt.exe" if os.name == "nt" else "gofmt"))
        if args.format:
            run("format-write", [gofmt, "-w"] + files)
        formatting = run("format-check", [gofmt, "-l"] + files, stdout="gofmt.txt")
        if (output / "gofmt.txt").read_text().strip():
            formatting["passed"] = False
            formatting["error"] = "unformatted CLI files; rerun the gate with --format and commit the resulting source diff"
            run("format-diff", [gofmt, "-d"] + files, stdout="formatting.diff")
        summary["source_sha256"] = {
            name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest()
            for name in files + ["go.mod", "go.sum", "scripts/ci/gregale_recovery_gate.py"]
        }
        # Keep compilation/test evidence even if formatting failed.
        with tempfile.TemporaryDirectory(prefix="gregale-recovery-build-") as temporary:
            binary = Path(temporary) / ("gregale.test.exe" if os.name == "nt" else "gregale.test")
            compile_stage = run("compile", [go, "test", "-p=1", GCFLAGS, "-c", "-o", str(binary), PACKAGE])
            if not compile_stage["passed"]:
                return 1
            discovery = run("discovery", [str(binary), "-test.list=" + FILTER], timeout=60)
            discovered = set((output / "discovery.log").read_text().splitlines())
            if not REQUIRED <= discovered:
                discovery["passed"] = False
                discovery["error"] = "required recovery acceptance tests are missing from the compiled binary"
            if not discovery["passed"]:
                return 1
            for name, extra, test_timeout, wall_timeout in (
                ("tests", [], "10m", 900), ("race", ["-race"], "15m", 1200)):
                stage = run(name, [go, "test", "-p=1", GCFLAGS] + extra +
                            ["-json", "-count=1", "-timeout=" + test_timeout, "-run", FILTER, PACKAGE],
                            timeout=wall_timeout, stdout=name + ".json")
                details = test_events(output / (name + ".json"))
                stage["tests"] = details
                stage["passed"] = stage["passed"] and not details["errors"]
                if not stage["passed"]:
                    print(name + ": " + "; ".join(details["errors"]), flush=True)
                    # Full JSON stays in the artifact; expose bounded failed-test
                    # output in job logs for diagnosis without downloading a ZIP.
                    shown = 0
                    for line in (output / (name + ".json")).read_text().splitlines():
                        try:
                            event = json.loads(line)
                        except json.JSONDecodeError:
                            continue
                        if isinstance(event, dict) and event.get("Action") == "output" and event.get("Test") in details["failed"]:
                            for message in event.get("Output", "").splitlines():
                                if shown < 200:
                                    print(message, flush=True)
                                    shown += 1
        summary["passed"] = all(stage["passed"] for stage in stages)
        return 0 if summary["passed"] else 1
    except (KeyError, ValueError, StopIteration, OSError) as error:
        summary["error"] = str(error)
        return 1
    finally:
        save()
        print("gregale-recovery evidence: " + str(output), flush=True)


if __name__ == "__main__":
    sys.exit(main())
