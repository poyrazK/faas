#!/usr/bin/env python3
"""Check guarded native HTTP evidence; this report never enables admission."""
import argparse
import hashlib
import json
import re
from pathlib import Path

SCENARIOS = {
    "TestProjectProgressiveHTTPPromotionMetal": (False, False, "succeeded"),
    "TestProjectProgressiveHTTPRollbackMetal": (True, False, "rolled_back"),
    "TestProjectProgressiveHTTPStabilizationRollbackMetal": (True, True, "rolled_back"),
}

FAILURE_SCENARIOS = {
    "TestProjectProgressiveHTTPMissingEvidenceTimeoutMetal": ("missing_evidence", False, "rollback_failed"),
    "TestProjectProgressiveHTTPEarlyAbortReplayMetal": ("early_abort", True, "rolled_back"),
    "TestProjectProgressiveHTTPFullAbortRecoveryReplayMetal": ("full_recover", True, "rolled_back"),
}

SERVICE_SCENARIOS = {
    "TestProjectProgressiveServiceGraphPromotionMetal": ("promotion", "succeeded"),
    "TestProjectProgressiveServiceGraphRollbackMetal": ("rollback", "rolled_back"),
    "TestProjectProgressiveServiceGraphAbortMetal": ("abort", "rolled_back"),
}

CONCURRENT_SCENARIOS = {
    "TestProjectProgressiveConcurrentEarlyWinnerMetal": ("concurrent_early", 5),
    "TestProjectProgressiveConcurrentFullWinnerMetal": ("concurrent_full", 100),
    "TestProjectProgressiveConcurrentConfigChangeMetal": ("concurrent_config", 5),
}

RETENTION_SCENARIOS = {
    "TestProjectRollbackParkedColdBootMetal": "scenario=cold_boot parked=true cold_boot=true restart=true graph_config=restored status=rolled_back",
    "TestProjectRollbackExpiredBaselineMetal": "scenario=expired clock=real recovery=denied restart=true graph_config=preserved status=rollback_failed",
}

SECRET_SCENARIOS = {
    "TestProjectReleaseSecretsPromotionMetal": "scenario=promotion stages=5,25,100 restart=true encrypted=true rekey=true old_key_removed=true cold_boot=true secret_config=matched status=succeeded",
    "TestProjectReleaseSecretsRollbackMetal": "scenario=rollback stages=5,25,100 restart=true encrypted=true rekey=true old_key_removed=true cold_boot=true secret_config=matched status=rolled_back",
}


def qualify(log, sha, scope="project-progressive-http-only"):
    required = {"project-progressive-http-only": SCENARIOS | FAILURE_SCENARIOS | SERVICE_SCENARIOS, "project-concurrent-release-only": CONCURRENT_SCENARIOS, "project-rollback-retention-only": RETENTION_SCENARIOS, "project-release-secrets-only": SECRET_SCENARIOS}.get(scope, {})
    errors = []
    if scope not in ("project-progressive-http-only", "project-concurrent-release-only", "project-rollback-retention-only", "project-release-secrets-only"):
        errors.append("invalid qualification scope")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        errors.append("invalid source revision")
    lines = log.splitlines()
    current = None
    observed = set()
    passed = set()
    for line in lines:
        start = re.match(r"^=== RUN   (\w+)$", line)
        if start:
            current = start[1]
        result = re.match(r"^--- (PASS|FAIL|SKIP): (\w+)(?: |$)", line)
        if result:
            if result[1] != "PASS":
                errors.append(f"{result[2]} {result[1]}")
            elif result[2] in required:
                if result[2] in passed:
                    errors.append(f"duplicate result: {result[2]}")
                passed.add(result[2])
            current = None
        if "progressive native:" in line and current in SCENARIOS:
            rollback, stabilize, status = SCENARIOS[current]
            expected = (f"stages=5,25,100 restart=true rollback={str(rollback).lower()} "
                        f"stabilization={str(stabilize).lower()} status={status} http_only=true")
            if re.search(r"attempt=[0-9a-f]{32} " + re.escape(expected) + r"$", line):
                observed.add(current)
        if "progressive failure native:" in line and current in FAILURE_SCENARIOS:
            scenario, replay, status = FAILURE_SCENARIOS[current]
            expected = (f"scenario={scenario} restart=true replay={str(replay).lower()} "
                        f"status={status} http_only=true")
            if re.search(r"attempt=[0-9a-f]{32} " + re.escape(expected) + r"$", line):
                observed.add(current)
        if "service graph native:" in line and current in SERVICE_SCENARIOS:
            scenario, status = SERVICE_SCENARIOS[current]
            stages = "" if scenario == "abort" else "stages=5,25,100 "
            expected = f"scenario={scenario} {stages}restart=true downstream=true status={status}"
            if line.endswith("service graph native: " + expected):
                observed.add(current)
        if "concurrent deploy native:" in line and current in CONCURRENT_SCENARIOS:
            scenario, percent = CONCURRENT_SCENARIOS[current]
            expected = (f"scenario={scenario} percent={percent} restart=true "
                        "stale_controls=denied winner_preserved=true status=conflicted")
            if line.endswith("concurrent deploy native: " + expected):
                observed.add(current)
        if "rollback retention native:" in line and current in RETENTION_SCENARIOS:
            if line.endswith("rollback retention native: " + RETENTION_SCENARIOS[current]):
                observed.add(current)
        if "release secrets native:" in line and current in SECRET_SCENARIOS:
            if line.endswith("release secrets native: " + SECRET_SCENARIOS[current]):
                observed.add(current)
    for name in required:
        if name not in passed or name not in observed:
            errors.append(f"missing PASS or scenario observations: {name}")
    if lines.count("native HTTP qualification: final_leakcheck_rc=0") != 1:
        errors.append("missing clean final leakcheck")
    if lines.count(f"native HTTP qualification: sha={sha} final_rc=0") != 1:
        errors.append("missing successful source-bound runner completion")
    return {"schema_version": 1, "source_sha": sha,
            "log_sha256": hashlib.sha256(log.encode()).hexdigest(),
            "scope": scope, "qualified": not errors,
            "scenarios": sorted(passed & observed), "errors": errors}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--log", type=Path, required=True)
    parser.add_argument("--sha", required=True)
    parser.add_argument("--scope", default="project-progressive-http-only", choices=["project-progressive-http-only", "project-concurrent-release-only", "project-rollback-retention-only", "project-release-secrets-only"])
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        report = qualify(args.log.read_text(), args.sha, args.scope)
    except OSError as exc:
        report = {"schema_version": 1, "source_sha": args.sha,
                  "qualified": False, "errors": [str(exc)]}
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    return 0 if report["qualified"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
