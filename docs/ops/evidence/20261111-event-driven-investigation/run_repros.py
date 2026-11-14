#!/usr/bin/env python3
"""Run permanent regressions, or overlay the original investigation fixtures."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("suite", choices=("api", "broker", "recovery", "bridge", "routing-controls"))
    parser.add_argument("--store", choices=("both", "memory", "postgres"), default="both",
                        help="Select the API, recovery or routing-controls suite's storage backend")
    args = parser.parse_args()
    evidence = Path(__file__).resolve().parent
    repo = evidence.parents[3]
    replacements = {}
    if args.suite == "api":
        package = "./pkg/apid/appevents"
        replacements[str(repo / "pkg/apid/appevents/zz_event_audit_repro_test.go")] = str(evidence / "appevents_repro_test.go.txt")
    elif args.suite == "recovery":
        package = "./pkg/sched"
        replacements[str(repo / "pkg/sched/zz_event_recovery_audit_test.go")] = str(evidence / "recovery_repro_test.go.txt")
    elif args.suite == "routing-controls":
        package = "./pkg/sched"
        replacements[str(repo / "pkg/sched/zz_event_routing_controls_audit_test.go")] = str(evidence / "routing_controls_repro_test.go.txt")
    elif args.suite == "bridge":
        package = "./pkg/vmmdgrpc"
        replacements[str(repo / "pkg/vmmdgrpc/zz_event_bridge_headers_audit_test.go")] = str(evidence / "bridge_headers_repro_test.go.txt")
    else:
        package = "./pkg/sched"
        replacements[str(repo / "pkg/sched/zz_event_audit_export_test.go")] = str(evidence / "export_test.go.txt")
        replacements[str(repo / "pkg/sched/zz_event_audit_repro_test.go")] = str(evidence / "repro_test.go.txt")
    permanent = {
        "api": ("pkg/apid/appevents/event_reliability_test.go", "^TestEventReliability"),
        "broker": ("pkg/sched/broker_reliability_test.go", "^TestBrokerReliability"),
        "recovery": ("pkg/sched/external_replay_reliability_test.go", "^TestEventReliabilityExternalDeadLetter"),
        "routing-controls": ("pkg/sched/event_routing_reliability_test.go", "^TestEventReliability(PermanentRouting|CircuitProbe|NATSToken)"),
    }
    pattern = "^TestAudit"
    if args.suite in permanent:
        filename, current_pattern = permanent[args.suite]
        if (repo / filename).is_file():
            replacements = {}
            pattern = current_pattern
    if args.suite in ("broker", "bridge") and args.store != "both":
        parser.error("--store applies only to the api, recovery and routing-controls suites")
    if args.suite in ("api", "recovery", "routing-controls") and args.store != "both":
        pattern += "/" + args.store
    env = os.environ.copy()
    toolchain = re.search(r"^toolchain (\S+)$", (repo / "go.mod").read_text(), re.MULTILINE)
    if toolchain:
        env["GOTOOLCHAIN"] = toolchain.group(1)
    env.setdefault("GOMEMLIMIT", "6GiB")
    with tempfile.TemporaryDirectory(prefix="gregale-event-audit-") as directory:
        overlay = Path(directory) / "overlay.json"
        overlay.write_text(json.dumps({"Replace": replacements}))
        command = ["go", "test", "-trimpath"]
        if replacements:
            command.append(f"-overlay={overlay}")
        if args.suite in ("broker", "recovery", "routing-controls"):
            command.append("-gcflags=github.com/onebox-faas/faas/pkg/sched=-c=1 -dwarf=false")
        command.extend([package, "-run", pattern, "-count=1", "-v"])
        return subprocess.run(command, cwd=repo, env=env, check=False).returncode


if __name__ == "__main__":
    raise SystemExit(main())
