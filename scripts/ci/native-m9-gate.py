#!/usr/bin/env python3
"""Native-only migration gate. Portable tests exercise the verdict, never faults.

spec: §14.A
adr: 1530
"""
import base64
import argparse
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import signal
import stat
import subprocess
import sys
import tempfile
import uuid
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = "github.com/onebox-faas/faas/tests/native/m9"
TEST = "TestMigrationAcknowledgementRecovery"
PHASES = ("preflight", "fixture-boot", "lost-acknowledgement", "vmmd-restart",
          "receipt-replay", "replacement-protection", "fixture-cleanup")
DRAIN_TEST = "TestNodeDrainControllerRecovery"
DRAIN_PHASES = ("preflight", "fixture-boot", "drain-pending", "schedd-restart",
                "automatic-migration", "drain-completion", "fixture-cleanup")


def need(condition, message):
    if not condition:
        raise ValueError(message)


def canonical_uuid(value):
    try:
        return isinstance(value, str) and str(uuid.UUID(value)) == value and uuid.UUID(value).int != 0
    except ValueError:
        return False


def object_pairs(pairs):
    result = {}
    for key, value in pairs:
        need(key not in result, "duplicate JSON key")
        result[key] = value
    return result


def decode(raw):
    return json.loads(raw, object_pairs_hook=object_pairs)


def configuration(raw):
    need(len(raw) <= 65536, "configuration exceeds 64 KiB")
    c = decode(raw)
    need(isinstance(c, dict) and set(c) <= {"version", "app_id", "deployment_id", "fixture_id",
         "timeout_seconds", "source", "destination", "drain"}, "unknown configuration fields")
    need(type(c.get("version")) is int and c["version"] == 1, "configuration version must be 1")
    for key in ("app_id", "deployment_id", "fixture_id"):
        need(canonical_uuid(c.get(key)), "canonical fixture UUIDs required")
    c.setdefault("timeout_seconds", 180)
    need(type(c["timeout_seconds"]) is int and 60 <= c["timeout_seconds"] <= 300,
         "timeout must be 60..300 seconds")
    for key in ("source", "destination"):
        peer = c.get(key)
        need(isinstance(peer, dict) and set(peer) == {"name", "ssh", "socket", "vmmd_sha256"},
             "complete peer configuration required")
        for field in ("name", "ssh"):
            need(isinstance(peer[field], str) and re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,253}", peer[field]),
                 "unsafe peer name or SSH alias")
        sock = peer["socket"]
        need(isinstance(sock, str) and re.fullmatch(r"/run/faas/[a-zA-Z0-9_/.-]+\.sock", sock)
             and os.path.normpath(sock) == sock, "unsafe peer socket")
        need(isinstance(peer["vmmd_sha256"], str) and re.fullmatch(r"[0-9a-f]{64}", peer["vmmd_sha256"]),
             "reviewed vmmd SHA-256 required")
    a, b = c["source"], c["destination"]
    need(a["name"] != b["name"] and a["ssh"] != b["ssh"], "two distinct peers required")
    need(a["vmmd_sha256"] == b["vmmd_sha256"], "peer vmmd artifacts differ")
    if "drain" in c:
        d = c["drain"]
        need(isinstance(d, dict) and set(d) == {"api_url", "schedd_sha256"}, "complete drain configuration required")
        need(isinstance(d["api_url"], str) and not any(ch.isspace() for ch in d["api_url"]), "unsafe drain API origin")
        u = urlsplit(d["api_url"])
        if u.port is not None:
            need(0 < u.port <= 65535, "invalid drain API port")
        need(u.scheme == "https" and u.hostname and u.username is None and u.password is None
             and u.path in {"", "/"} and not u.query and not u.fragment and "?" not in d["api_url"]
             and "#" not in d["api_url"], "drain API must be an HTTPS origin without credentials, query or fragment")
        need(isinstance(d["schedd_sha256"], str) and re.fullmatch(r"[0-9a-f]{64}", d["schedd_sha256"]),
             "reviewed schedd SHA-256 required")
    return c


def leak_proof(value, bundle):
    need(isinstance(value, dict) and value.get("passed") is True and value.get("bundle_sha256") == bundle,
         "missing or stale peer leak proof")


def daemon_proof(value, digest, key="vmmd_sha256"):
    need(isinstance(value, dict) and type(value.get("pid")) is int and value["pid"] > 0
         and isinstance(value.get("start_ticks"), str) and value["start_ticks"].isdigit()
         and isinstance(value.get("invocation_id"), str) and re.fullmatch(r"[0-9a-f]{32}", value["invocation_id"])
         and value.get(key) == digest, "invalid reviewed daemon identity")


def verify_events(lines, run_id, revision, config, bundle, scenario="migration"):
    """Require one complete package/root/subtest stream and bound phase evidence."""
    test_name, phases = (DRAIN_TEST, DRAIN_PHASES) if scenario == "drain" else (TEST, PHASES)
    states, proofs, order = {}, {}, []
    allowed = {test_name, *(test_name + "/" + phase for phase in phases)}
    package_pass = False
    for line in lines:
        event = decode(line)
        action, test = event.get("Action"), event.get("Test", "")
        need(event.get("Package") == PACKAGE, "unexpected test package")
        need(not package_pass, "events after package verdict")
        need(action not in {"fail", "skip"}, "required native test failed or skipped")
        if test:
            need(test in allowed, "unexpected native subtest")
            if action == "run":
                need(test not in states, "duplicate test run")
                states[test] = "run"
                if test != test_name:
                    need(states.get(test_name) == "run", "subtest outside root lifetime")
                    order.append(test.removeprefix(test_name + "/"))
            elif action == "pass":
                need(states.get(test) == "run", "pass without unique run")
                if test == test_name:
                    need(all(states.get(test_name + "/" + p) == "pass" for p in phases), "root passed before phases")
                else:
                    need(test.removeprefix(test_name + "/") in proofs, "phase passed without evidence")
                states[test] = "pass"
            elif action == "output" and "M9_EVIDENCE " in event.get("Output", ""):
                proof = decode(event["Output"].split("M9_EVIDENCE ", 1)[1].strip())
                phase = proof.get("phase")
                need(phase in phases and test == test_name + "/" + phase and states.get(test) == "run"
                     and phase not in proofs, "misplaced or repeated phase proof")
                need(proof.get("version") == 1 and proof.get("run_id") == run_id
                     and proof.get("revision") == revision and isinstance(proof.get("details"), dict)
                     and proof["details"], "foreign, stale or empty phase evidence")
                proofs[phase] = proof["details"]
        elif action == "pass":
            need(states.get(test_name) == "pass", "package passed without native root test")
            package_pass = True
    need(package_pass and order == list(phases) and set(proofs) == set(phases), "incomplete native test stream")
    if scenario == "drain":
        verify_drain_proofs(proofs, config, bundle)
    else:
        verify_proofs(proofs, config, bundle)
    return proofs


def verify_preflight(pre, c, bundle):
    need(pre.get("app_id") == c["app_id"] and pre.get("deployment_id") == c["deployment_id"], "wrong fixture")
    peers = pre.get("peers", {})
    need(set(peers) == {c["source"]["name"], c["destination"]["name"]}, "missing peer attestation")
    boots, nodes = set(), set()
    for peer in (c["source"], c["destination"]):
        proof = peers[peer["name"]]
        need(canonical_uuid(proof.get("boot_id")) and canonical_uuid(proof.get("node_id"))
             and canonical_uuid(proof.get("runtime_incarnation")) and proof.get("firecracker_version"), "invalid peer identity")
        daemon_proof(proof.get("initial"), peer["vmmd_sha256"])
        boots.add(proof["boot_id"])
        nodes.add(proof["node_id"])
        leak_proof(proof.get("leaks_before"), bundle)
    need(len(boots) == 2 and len(nodes) == 2, "peers resolve to the same host or node")
    return peers


def verify_proofs(p, c, bundle):
    peers = verify_preflight(p["preflight"], c, bundle)
    boot, lost = p["fixture-boot"], p["lost-acknowledgement"]
    source, destination = peers[c["source"]["name"]], peers[c["destination"]["name"]]
    before, after = lost.get("source_before_ack", {}), lost.get("source_after_ack", {})
    binding = ("InstanceID", "SourceNodeID", "SourceWakeID", "LeaseToken", "NativeGeneration", "PhysicalBinding",
               "RAMMB", "CPUMillicores", "VCPU")
    need(all(key in before and before[key] == after.get(key) for key in binding), "source binding changed")
    need(all(canonical_uuid(before.get(key)) for key in ("InstanceID", "SourceNodeID", "SourceWakeID", "NativeGeneration"))
         and isinstance(before["PhysicalBinding"], str) and re.fullmatch(r"[0-9a-f]{64}", before["PhysicalBinding"])
         and isinstance(before["LeaseToken"], str) and before["LeaseToken"] and before["VCPU"] > 0,
         "missing original physical source identity")
    need(before.get("Phase") == "retiring" and before.get("Proof") == "" and after.get("Phase") == "retired"
         and after.get("Proof") and lost.get("response_discarded") is True, "missing real source retirement and lost response")
    need(canonical_uuid(boot.get("instance_id")) and before["InstanceID"] == boot["instance_id"] == lost.get("instance_id")
         and before["SourceNodeID"] == boot.get("node_id") == source["node_id"]
         and before["SourceWakeID"] == boot.get("wake_id") and lost.get("destination_node_id") == destination["node_id"]
         and canonical_uuid(lost.get("destination_wake_id")) and lost["destination_wake_id"] != boot["wake_id"], "ownership evidence mismatch")
    capacity = lost.get("retained_capacity", {})
    need(before["RAMMB"] > 0 and before["CPUMillicores"] > 0
         and capacity.get("ram_mb", -1) >= before["RAMMB"]
         and capacity.get("cpu_millicores", -1) >= before["CPUMillicores"], "source capacity was not retained")
    probe = boot.get("probe", {})
    need(probe.get("fixture_id") == c["fixture_id"] and canonical_uuid(probe.get("boot_id"))
         and canonical_uuid(probe.get("operation_id")) and probe.get("sequence", 0) > 0
         and probe.get("unique_operations", 0) >= probe["sequence"], "missing live operation evidence")
    need(lost.get("probe") == probe and p["receipt-replay"].get("probe") == probe, "workload changed or replay executed twice")
    restart = p["vmmd-restart"]
    daemon_proof(restart.get("restarted"), c["source"]["vmmd_sha256"])
    need(restart.get("killed") == source["initial"] and restart.get("restarted", {}).get("vmmd_sha256") == c["source"]["vmmd_sha256"]
         and restart["restarted"].get("invocation_id") != source["initial"].get("invocation_id")
         and canonical_uuid(restart.get("runtime_incarnation"))
         and restart["runtime_incarnation"] != source["runtime_incarnation"], "source daemon did not restart")
    need(restart.get("retained_source") == after == p["receipt-replay"].get("source"), "retained receipt changed across restart/replay")
    replacement = p["replacement-protection"]
    next_probe = replacement.get("probe", {})
    need(replacement.get("instance_id") == boot["instance_id"] and canonical_uuid(replacement.get("wake_id"))
         and replacement["wake_id"] not in {boot["wake_id"], lost["destination_wake_id"]}
         and canonical_uuid(replacement.get("native_generation")) and replacement["native_generation"] != before["NativeGeneration"]
         and replacement.get("old_source_generation") == before["NativeGeneration"], "replacement generation not protected")
    need(next_probe.get("fixture_id") == c["fixture_id"] and next_probe.get("boot_id") == probe["boot_id"]
         and canonical_uuid(next_probe.get("operation_id")) and next_probe["operation_id"] != probe["operation_id"]
         and next_probe.get("sequence") == probe["unique_operations"] + 1
         and next_probe.get("unique_operations") == next_probe["sequence"], "return migration lost or duplicated operation evidence")
    cleanup = p["fixture-cleanup"]
    need(cleanup.get("instance_id") == boot["instance_id"] and set(cleanup.get("leaks_after", {})) == set(peers), "incomplete fixture cleanup")
    for value in cleanup["leaks_after"].values():
        leak_proof(value, bundle)


def verify_drain_proofs(p, c, bundle):
    peers = verify_preflight(p["preflight"], c, bundle)
    source, dest = peers[c["source"]["name"]], peers[c["destination"]["name"]]
    digest = c["drain"]["schedd_sha256"]
    for peer in peers.values():
        daemon_proof(peer.get("scheduler", {}).get("initial"), digest, "schedd_sha256")
    boot, pending = p["fixture-boot"], p["drain-pending"]
    before = p["preflight"].get("source_generation")
    generation = pending.get("generation")
    need(type(before) is int and before > 0 and generation == before + 1, "wrong drain cycle generation")
    need(canonical_uuid(boot.get("instance_id")) and canonical_uuid(boot.get("wake_id"))
         and boot.get("node_id") == source["node_id"], "fixture did not boot on source")
    probe = boot.get("probe", {})
    need(probe.get("fixture_id") == c["fixture_id"] and canonical_uuid(probe.get("boot_id"))
         and canonical_uuid(probe.get("operation_id")) and probe.get("sequence", 0) > 0
         and probe.get("unique_operations", 0) >= probe["sequence"], "missing live fixture receipt")
    need(pending.get("instance_id") == boot["instance_id"] and pending.get("source_node_id") == source["node_id"]
         and pending.get("lifecycle") == "draining" and pending.get("placement_refused") is True
         and pending.get("completion_absent") is True and pending.get("ram_mb", 0) > 0
         and pending.get("observed_completed_at") == p["preflight"].get("previous_drain_completed_at")
         and pending.get("cpu_millicores", 0) > 0 and pending.get("probe") == probe,
         "drain did not remain pending with fenced placement and retained live work")
    initial = dest["scheduler"]["initial"]
    paused, restart = pending.get("paused_scheduler", {}), p["schedd-restart"]
    need(paused.get("paused") is True and paused.get("daemon") == initial, "wrong paused controller")
    daemon_proof(restart.get("restarted"), digest, "schedd_sha256")
    need(restart.get("killed") == initial and restart["restarted"]["invocation_id"] != initial["invocation_id"]
         and restart.get("drain_generation") == generation, "controller did not restart during this drain")
    moved, done = p["automatic-migration"], p["drain-completion"]
    need(moved.get("instance_id") == boot["instance_id"] and moved.get("node_id") == dest["node_id"]
         and canonical_uuid(moved.get("wake_id")) and moved["wake_id"] != boot["wake_id"]
         and moved.get("probe") == probe, "automatic migration lost its workload or duplicated the operation")
    original, retired = moved.get("source", {}), done.get("source", {})
    binding = ("InstanceID", "SourceNodeID", "SourceWakeID", "LeaseToken", "NativeGeneration", "PhysicalBinding",
               "RAMMB", "CPUMillicores", "VCPU")
    need(all(key in original and original[key] == retired.get(key) for key in binding), "physical source binding changed")
    need(original.get("InstanceID") == boot["instance_id"] and original.get("SourceNodeID") == source["node_id"]
         and original.get("SourceWakeID") == boot["wake_id"] and original.get("LeaseToken")
         and canonical_uuid(original.get("NativeGeneration")) and re.fullmatch(r"[0-9a-f]{64}", original.get("PhysicalBinding", ""))
         and original.get("Phase") in {"retiring", "retired"} and retired.get("Phase") == "retired" and retired.get("Proof")
         and original.get("RAMMB", 0) > 0 and original.get("CPUMillicores", 0) > 0 and original.get("VCPU", 0) > 0,
         "drain completed without the original physical source retirement")
    receipt, progress = done.get("receipt", {}), done.get("api", {})
    need(receipt.get("NodeID") == source["node_id"] and receipt.get("NodeName") == c["source"]["name"]
         and receipt.get("SourceGeneration") == generation and receipt.get("ResultGeneration") == generation + 1
         and receipt.get("SourceLifecycle") == "draining" and receipt.get("ResultLifecycle") == "maintenance"
         and receipt.get("AuditOutboxID", 0) > 0 and done.get("receipt_unchanged") is True
         and done.get("source_empty") is True, "missing immutable completion and audit handoff")
    completed = datetime.fromisoformat(receipt["CompletedAt"].replace("Z", "+00:00"))
    initiated = datetime.fromisoformat(receipt["InitiatedAt"].replace("Z", "+00:00"))
    api_completed = datetime.fromisoformat(progress["drain_completed_at"].replace("Z", "+00:00"))
    api_initiated = datetime.fromisoformat(progress["drain_initiated_at"].replace("Z", "+00:00"))
    need(completed.tzinfo is not None and initiated.tzinfo is not None and completed >= initiated
         and api_completed == completed and api_initiated == initiated and progress.get("lifecycle") == "maintenance"
         and progress.get("node_name") == c["source"]["name"], "completion API and receipt disagree")
    need(0 < moved.get("elapsed_ms", 0) <= done.get("elapsed_ms", 0) <= c["timeout_seconds"] * 1000,
         "automatic drain exceeded its qualification budget")
    cleanup = p["fixture-cleanup"]
    need(cleanup.get("instance_id") == boot["instance_id"] and cleanup.get("source_lifecycle") == "maintenance"
         and set(cleanup.get("leaks_after", {})) == set(peers), "incomplete drain fixture cleanup")
    for value in cleanup["leaks_after"].values():
        leak_proof(value, bundle)


def command(args, timeout=30, **kwargs):
    return subprocess.run(args, check=True, text=True, capture_output=True, timeout=timeout, **kwargs).stdout.strip()


def preflight(c, revision):
    need(os.geteuid() == 0 and platform.system() == "Linux" and platform.machine() == "x86_64", "root on designated x86_64 Linux required")
    info = Path("/etc/faas/m9-acceptance-host").lstat()
    need(stat.S_ISREG(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o022, "unsafe control-plane acceptance marker")
    need(os.environ.get("FAAS_M9_CONFIRM") == "native-x86", "FAAS_M9_CONFIRM=native-x86 required")
    need(os.environ.get("DATABASE_URL"), "DATABASE_URL required")
    need(not command(["git", "status", "--porcelain"], cwd=ROOT), "qualification requires a clean checkout")
    go = os.environ.get("GO", "go")
    expected = re.search(r"(?m)^toolchain (go\S+)$", (ROOT / "go.mod").read_text()).group(1)
    need(command([go, "env", "GOVERSION"], cwd=ROOT) == expected, "checkout's pinned Go toolchain required")
    digest = verify_binary(Path(os.environ["FAAS_M9_VMMD_BINARY"]), c["source"]["vmmd_sha256"], revision, go)
    result = {"vmmd_sha256": digest, "go_version": expected}
    if c.get("drain"):
        need(os.environ.get("FAAS_M9_OPERATOR_TOKEN"), "FAAS_M9_OPERATOR_TOKEN required for drain")
        result["schedd_sha256"] = verify_binary(Path(os.environ["FAAS_M9_SCHEDD_BINARY"]), c["drain"]["schedd_sha256"], revision, go)
    return result


def verify_binary(binary, expected_digest, revision, go):
    with binary.open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha256").hexdigest()
    need(digest == expected_digest, "local reviewed daemon artifact differs from configuration")
    build = command([go, "version", "-m", str(binary)])
    settings = {}
    for line in build.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[0] == "build" and "=" in fields[1]:
            key, value = fields[1].split("=", 1)
            need(key not in settings, "duplicate artifact build metadata")
            settings[key] = value
    need(settings.get("vcs.revision") == revision, "daemon artifact was not built from this checkout")
    need(settings.get("vcs.modified") == "false", "daemon artifact was built from a modified checkout")
    return digest


def peer_action(peer, run_id, action, files):
    request = {"run_id": run_id, "vmmd_sha256": peer["vmmd_sha256"], "action": action, "leak_files": files}
    if peer.get("schedd_sha256"):
        request["schedd_sha256"] = peer["schedd_sha256"]
    payload = base64.b64encode(json.dumps(request).encode()).decode()
    result = command(["ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", peer["ssh"],
                      "sudo", "-n", "python3", "-", payload],
                     input=(ROOT / "tests/native/m9/native_m9_peer.py").read_text(), timeout=60)
    return decode(result)


def run_test(c, run_id, revision, config_path, evidence, scenario="migration"):
    env = dict(os.environ, FAAS_M9_QUALIFY="1", FAAS_M9_RUN_ID=run_id, FAAS_M9_REVISION=revision,
               FAAS_M9_CONFIG=str(config_path), FAAS_M9_SCENARIO=scenario)
    timeout = c["timeout_seconds"] + 120
    args = [os.environ.get("GO", "go"), "test", "-json", "-count=1", "-tags=metal", "./tests/native/m9",
            "-run", "^" + (DRAIN_TEST if scenario == "drain" else TEST) + "$", "-timeout=" + str(timeout) + "s"]
    process = None
    with (evidence / "go-test.jsonl").open("w") as output, (evidence / "go-test.stderr").open("w") as errors:
        try:
            process = subprocess.Popen(args, cwd=ROOT, env=env, stdout=output, stderr=errors, start_new_session=True)
            return process.wait(timeout=timeout + 60)
        finally:
            if process is not None and process.poll() is None:
                os.killpg(process.pid, signal.SIGINT)
                try:
                    process.wait(timeout=70)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=10)


def write_report(directory, report):
    path = directory / "report.json.tmp"
    path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    path.replace(directory / "report.json")


def main(scenario="migration"):
    os.umask(0o077)
    evidence = Path(tempfile.mkdtemp(prefix="gregale-native-m9-"))
    run_id = str(uuid.uuid4())
    report = {"version": 1, "run_id": run_id, "scenario": scenario, "status": "FAIL", "native_qualified": False, "peer_restoration": {}}
    attempted, config, files, passed = False, None, {}, False
    try:
        revision = command(["git", "rev-parse", "HEAD"], cwd=ROOT)
        report["revision"] = revision
        config_path = Path(os.environ["FAAS_M9_CONFIG"]).resolve(strict=True)
        info = config_path.stat()
        need(stat.S_ISREG(info.st_mode) and 0 < info.st_size <= 65536, "configuration must be a regular file of at most 64 KiB")
        with config_path.open("rb") as stream:
            raw = stream.read(65537)
        config = configuration(raw)
        need(scenario in {"migration", "drain"} and (scenario == "drain") == (config.get("drain") is not None),
             "selected scenario and drain configuration must match")
        report["config_sha256"] = hashlib.sha256(raw).hexdigest()
        report["artifact"] = preflight(config, revision)
        # Freeze the validated input; callers cannot swap the configuration mid-drill.
        config_path = evidence / "config.json"
        config_path.write_bytes(raw)
        files = {name: (ROOT / "deploy/scripts" / name).read_text() for name in ("leakcheck.sh", "leakcheck_native_loops.py")}
        bundle = {name: hashlib.sha256(body.encode()).hexdigest() for name, body in files.items()}
        attempted = True
        code = run_test(config, run_id, revision, config_path, evidence) if scenario == "migration" else \
            run_test(config, run_id, revision, config_path, evidence, scenario)
        report["test_exit_code"] = code
        need(code == 0, "native Go drill failed; inspect private test logs")
        with (evidence / "go-test.jsonl").open() as stream:
            report["phases"] = verify_events(stream, run_id, revision, config, bundle, scenario)
        need(command(["git", "rev-parse", "HEAD"], cwd=ROOT) == revision
             and not command(["git", "status", "--porcelain"], cwd=ROOT), "checkout changed during qualification")
        passed = True
    except (Exception, KeyboardInterrupt) as exc:
        # Never include DSNs, SSH stderr or a command's argument list in the public verdict.
        report["error"] = str(exc) if isinstance(exc, (ValueError, KeyError, FileNotFoundError)) else type(exc).__name__
    finally:
        # Finish bounded restoration even if the operator presses Ctrl-C again.
        for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            signal.signal(sig, signal.SIG_IGN)
        if attempted:
            peers = [dict(config[key], **({"schedd_sha256": config["drain"]["schedd_sha256"]} if scenario == "drain" else {}))
                     for key in ("source", "destination")]
            for peer in peers:
                try:
                    restored = peer_action(peer, run_id, "restore", files)
                    need(restored.get("restored") is True, "peer was not restored")
                    if scenario == "drain":
                        scheduler = restored.get("scheduler", {})
                        need(scheduler.get("restored") is True, "peer scheduler was not restored")
                        daemon_proof(scheduler.get("daemon"), config["drain"]["schedd_sha256"], "schedd_sha256")
                    leaks = peer_action(peer, run_id, "leaks", files)
                    leak_proof(leaks, bundle)
                    report["peer_restoration"][peer["name"]] = {**restored, "leaks": leaks}
                except Exception as exc:
                    report["peer_restoration"][peer["name"]] = {"error": type(exc).__name__, "lease_retained": True}
                    passed = False
            if passed:
                for peer in peers:
                    try:
                        result = peer_action(peer, run_id, "release", files)
                        need(result.get("released") is True, "peer lease not released")
                        leak_proof(result, bundle)
                        report["peer_restoration"][peer["name"]]["lease_released"] = True
                    except Exception as exc:
                        report["peer_restoration"][peer["name"]]["release_error"] = type(exc).__name__
                        passed = False
        report["status"], report["native_qualified"] = ("PASS", True) if passed else ("FAIL", False)
        write_report(evidence, report)
    print("native M9 " + scenario + ": " + report["status"] + "; evidence: " + str(evidence / "report.json"))
    return 0 if passed else 1


if __name__ == "__main__":
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt
    for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(sig, interrupted)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scenario", choices=("migration", "drain"), default="migration")
    sys.exit(main(parser.parse_args().scenario))
