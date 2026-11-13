#!/usr/bin/env python3
"""Collect read-only deployment monitoring evidence over pinned SSH.

The bearer is supplied on stdin to the selected release's CLI. Transport errors,
old binaries, missing credentials and interrupted runs leave an unknown artifact.
No deployment, notification, token creation or recovery command runs here.
"""

import argparse
import datetime as dt
import json
import math
import os
from pathlib import Path
import re
import shlex
import subprocess
import tempfile


SCOPE = "post_deployment_monitoring"
CHECKS = ("serving_probes", "notification_pipeline", "notification_receivers")
GUIDE = "https://github.com/poyrazK/faas/blob/main/docs/ops/post-deployment-monitoring.md"
# Match the health-response envelope limit in pkg/api/limits.go.
REPORT_MAX_BYTES = 1 << 20
SSH_TIMEOUT_SECONDS = 3620  # CLI maximum one-hour wait plus connection overhead.


def clock():
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def boundary(value):
    if not value:
        return None
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
        if parsed.tzinfo is None or parsed > dt.datetime.now(dt.timezone.utc) + dt.timedelta(seconds=30):
            return None
        # Restrict the accepted grammar to the Go CLI's RFC3339 input.
        if not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})", value):
            return None
        return parsed.astimezone(dt.timezone.utc).isoformat().replace("+00:00", "Z")
    except ValueError:
        return None


def fallback(release, after, reason):
    now = clock()
    return {
        "schema_version": 1, "scope": SCOPE, "requested_release": release,
        "evidence_after": after, "started_at": now, "updated_at": now,
        "wait_seconds": 0, "attempts": 0, "state": "unknown", "reason": reason,
        "had_confirmed_faults": False,
        "checks": [{"name": name, "state": "unknown", "reason": reason, "runbook": GUIDE} for name in CHECKS],
        "limitations": ["requested_release_not_runtime_identity", "verification_not_completed", "no_deployment_or_rollback_performed"],
    }


def write_report(path, report):
    name = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent, prefix=".monitoring-", delete=False) as output:
            name = output.name
            json.dump(report, output, allow_nan=False, indent=2)
            output.write("\n")
        os.replace(name, path)
    finally:
        if name and os.path.exists(name):
            os.unlink(name)


def reject_constant(_):
    raise ValueError("nonfinite JSON")


def finite_float(value):
    result = float(value)
    if not math.isfinite(result):
        raise ValueError("nonfinite JSON")
    return result


def report_valid(report, release, after, code):
    if not isinstance(report, dict) or report.get("schema_version") != 1 or report.get("scope") != SCOPE:
        return False
    if report.get("requested_release") != release or boundary(report.get("evidence_after")) != after:
        return False
    if report.get("state") != {0: "passed", 1: "degraded", 3: "unknown"}.get(code, "invalid"):
        return False
    checks = report.get("checks")
    if not isinstance(checks, list) or len(checks) != len(CHECKS):
        return False
    for check, name in zip(checks, CHECKS):
        if not isinstance(check, dict) or check.get("name") != name or check.get("state") not in ("passed", "degraded", "unknown"):
            return False
        if not isinstance(check.get("reason"), str) or not re.fullmatch(r"[a-z_]{0,80}", check["reason"]):
            return False
    states = [check["state"] for check in checks]
    if code == 0 and states != ["passed"] * len(CHECKS):
        return False
    if code == 1 and "degraded" not in states:
        return False
    return isinstance(report.get("had_confirmed_faults"), bool) and isinstance(report.get("attempts"), int)


def collect(release, after, token, host, key):
    # Both active-pointer reads are read-only. Pin the binary path as well so
    # an overlapping rollout cannot silently change the verifier implementation.
    expected = "/opt/faas/releases/" + release
    args = ["env", "FAAS_APID_URL=http://127.0.0.1:8080", expected + "/bin/gregalectl", "obs", "verify-deployment", "--release", release,
            "--after", after, "--token-stdin", "--json"]
    pointer = 'test "$(readlink -f /opt/faas/current)" = ' + shlex.quote(expected)
    remote = pointer + " || exit 64\n" + shlex.join(args) + "\nresult=$?\n" + pointer + ' || exit 64\nexit "$result"'
    command = ["ssh", "-T", "-i", key, "-o", "StrictHostKeyChecking=yes", "-o", "BatchMode=yes",
               "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "root@" + host, remote]
    child_env = os.environ.copy()
    child_env.pop("MONITORING_OPERATOR_TOKEN", None)
    # stdout is private and size-checked before decoding; stderr may contain
    # provider/transport text and must never enter logs or uploaded artifacts.
    with tempfile.TemporaryFile() as output:
        try:
            result = subprocess.run(command, input=(token + "\n").encode(), stdout=output, stderr=subprocess.DEVNULL,
                                    timeout=SSH_TIMEOUT_SECONDS, check=False, env=child_env)
        except (OSError, subprocess.TimeoutExpired):
            return None, "transport_unavailable", 3
        if result.returncode == 64:
            return None, "active_release_changed", 3
        if result.returncode not in (0, 1, 3):
            return None, "verification_unavailable", 3
        if output.tell() > REPORT_MAX_BYTES:
            return None, "invalid_verification_report", 3
        output.seek(0)
        try:
            report = json.load(output, parse_constant=reject_constant, parse_float=finite_float)
            if not report_valid(report, release, after, result.returncode):
                return None, "invalid_verification_report", 3
        except (ValueError, TypeError, AttributeError, OverflowError, RecursionError):
            return None, "invalid_verification_report", 3
    return report, "", result.returncode


def summarize(report):
    text = "### Post-deployment monitoring\n\n"
    text += "Release: `" + report["requested_release"] + "`\n\n"
    text += "Result: **" + report["state"] + "**. See the monitoring evidence artifact and [operator guide](" + GUIDE + ").\n\n"
    for check in report["checks"]:
        text += "- " + check["name"] + ": " + check["state"] + (" (" + check["reason"] + ")" if check["reason"] else "") + "\n"
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as output:
            output.write(text)
    print("Post-deployment monitoring: " + report["state"] + ". See the evidence artifact.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--initialize", action="store_true")
    options = parser.parse_args()
    release = os.environ.get("DESIRED_RELEASE", "")
    if not re.fullmatch(r"[0-9a-f]{40}", release):
        print("Post-deployment monitoring: a full requested release SHA is required.")
        return 2
    path = Path(os.environ.get("MONITORING_REPORT", "post-deployment-monitoring.json"))
    after = boundary(os.environ.get("MONITORING_NOT_BEFORE", ""))
    report = fallback(release, after, "verification_not_started" if options.initialize else "verification_not_completed")
    write_report(path, report)
    if options.initialize:
        return 0
    token, host = os.environ.get("MONITORING_OPERATOR_TOKEN", ""), os.environ.get("CP_HOST", "")
    key = os.environ.get("MONITORING_SSH_KEY", os.path.expanduser("~/.ssh/deploy_key"))
    code = 3
    if after is None:
        report["reason"] = "rollout_boundary_unavailable"
    elif not token.strip() or len(token.encode()) > 4096 or any(char in token for char in "\r\n\x00"):
        report["reason"] = "operator_credential_unavailable"
    elif not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.:-]{0,252}", host):
        report["reason"] = "ssh_host_unavailable"
    else:
        result, reason, code = collect(release, after, token, host, key)
        if result is not None:
            report = result
        else:
            report["reason"] = reason
    if code == 3 and report["attempts"] == 0:
        for check in report["checks"]:
            check["reason"] = report["reason"]
    write_report(path, report)
    summarize(report)
    return code


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except OSError:
        # Never print local paths or subprocess errors, which can contain secrets.
        print("Post-deployment monitoring: evidence report could not be written.")
        raise SystemExit(2) from None
