"""Native campaign crash/transfer drill. Only public CLI operations mutate resources."""
import json
import os
from pathlib import Path
import signal
import subprocess
import time
import urllib.parse


def require(ok, reason):
    if not ok:
        raise ValueError(reason)


def read(path):
    return json.loads(Path(path).read_text())


def process_start(pid):
    # comm can contain spaces and parentheses; fields after its final ')' start at 3.
    return Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]


def pin_worker(record):
    identity = record["process"]
    require(record["state"] == "started", "worker has not started")
    pid = identity["pid"]
    require(identity["boot_id"] == Path("/proc/sys/kernel/random/boot_id").read_text().strip()
            and identity["pid_namespace"] == os.readlink("/proc/self/ns/pid"), "worker belongs to another host/namespace")
    fd = os.pidfd_open(pid)
    try:
        require(process_start(pid) == identity["start_ticks"], "worker PID was reused")
        signal.pidfd_send_signal(fd, 0)
        return fd
    except BaseException:
        os.close(fd)
        raise


def verify_uncertain(directory, batch):
    checkpoint = read(directory / "checkpoint.json")
    require(checkpoint["halted"] and checkpoint["batches"][batch]["status"] == "uncertain",
            "interrupted attempt lost its uncertain reservation")
    require(not (directory / f"batch-{batch}.json").exists(), "interrupted attempt gained a report")
    return checkpoint


def inspect_sessions(api, journal):
    for workload in journal["workloads"].values():
        code, session = api.get("/v1/dev/sessions/" + workload["project"] + "?" +
                                urllib.parse.urlencode({"workspace_id": journal["run_id"]}))
        require(code == 200 and session["app"]["id"] == workload["app_id"], "live session differs from journal")


def run(repo, out, cli, api, manifest, project, cleanup_probe):
    out.mkdir(mode=0o700)
    policy, plan, directory = out / "policy.yaml", out / "plan.json", out / "source"
    policy.write_text("schema_version: 1\nmin_executed_runs: 1\non_not_comparable: ignore\non_insufficient_samples: ignore\nrequired_coverage:\n  - name: native-crash\n    match: {scenario: tcp-campaign, profile: warm}\n    min_executed_runs: 1\n")
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()
    prefix = [str(cli), "test", "policy"]

    def command(name, arguments, allowed=(0,)):
        # All stdout/stderr stays in the private evidence directory, never console.
        with (out / (name + ".json")).open("w") as stdout, (out / (name + ".log")).open("w") as stderr:
            result = subprocess.run(prefix + arguments, cwd=repo, stdout=stdout, stderr=stderr, timeout=180)
        require(result.returncode in allowed, name + " returned unexpected exit status " + str(result.returncode))
        return result.returncode

    source = ["--plan", str(plan), "--policy", str(policy), "--manifest", str(manifest), "--directory", str(directory)]
    command("plan", ["plan", "--policy", str(policy), "--manifest", str(manifest), "--plan-max-runs", "1"])
    batches = read(plan)["batches"]
    require(len(batches) == 1, "crash drill requires exactly one planned batch")
    batch, side = batches[0]["id"], batches[0]["side"]
    require(side in ("baseline", "current"), "invalid planned revision side")
    runner = ["run", *source, "--side", side, "--revision", revision, "--concurrency", "1"]
    worker_fd, coordinator, journal, recipient = None, None, None, None
    result = {"passed": False, "batch_id": batch}
    try:
        with (out / "coordinator.log").open("w") as log:
            coordinator = subprocess.Popen(prefix + runner, cwd=repo, stdout=log, stderr=subprocess.STDOUT)
            deadline = time.monotonic() + 900
            while True:
                require(coordinator.poll() is None, "coordinator ended before crash barrier")
                records = list(directory.glob("runner-process-*/*.json"))
                if worker_fd is None and len(records) == 1:
                    record = read(records[0])
                    if record["state"] == "started":
                        worker_fd = pin_worker(record)
                barrier = False
                journals = list(directory.glob(f"batch-{batch}.journal/*.json"))
                for path in journals:
                    if path.name == "binding.json":
                        continue
                    try:
                        candidate = read(path)
                    except json.JSONDecodeError:
                        # The worker exclusively creates the run file before its
                        # first atomic save. Never use a partial journal as proof.
                        continue
                    workloads = candidate["workloads"]
                    if len(workloads) != 2 or not all(w.get("app_id") and w["state"] == "provisioned" for w in workloads.values()):
                        continue
                    journal = candidate
                    code, matches = api.get("/v1/dev/test-runs/" + journal["run_id"] + "/chaos/matches")
                    if code != 200 or not any(m.get("count", 0) > 0 for m in matches.get("matches", [])):
                        continue
                    require(len(records) == 1, "crash drill requires one tracked worker")
                    require(worker_fd is not None, "worker identity has not been pinned")
                    inspect_sessions(api, journal)
                    result["fault_matches"] = matches
                    barrier = True
                    break
                if barrier:
                    break
                require(time.monotonic() < deadline, "native fault/worker crash barrier timed out")
                time.sleep(.1)
            coordinator.kill()
            coordinator.wait(timeout=15)
        command("live-worker-blocked", ["recover-lock", "--plan", str(plan), "--directory", str(directory), "--execute"], (2,))
        require((directory / "runner.lock").exists(), "live worker lost its lock")
        signal.pidfd_send_signal(worker_fd, signal.SIGKILL)
        deadline = time.monotonic() + 30
        while True:
            command("terminated-inspection", ["recover-lock", "--plan", str(plan), "--directory", str(directory)])
            if read(out / "terminated-inspection.json")["recoverable"]:
                break
            require(time.monotonic() < deadline, "killed worker did not become recoverable")
            time.sleep(.1)
        command("recover-lock", ["recover-lock", "--plan", str(plan), "--directory", str(directory), "--execute"])
        verify_uncertain(directory, batch)
        source_checkpoint = (directory / "checkpoint.json").read_bytes()
        command("source-resume-blocked", runner, (1,))
        require((directory / "checkpoint.json").read_bytes() == source_checkpoint, "rejected source resume changed checkpoint")
        verify_uncertain(directory, batch)
        bundle = out / "handoff.json"
        command("export", ["bundle", "export", *source, "--output", str(bundle)])
        claims = out / "claims"
        claims.mkdir(mode=0o700)
        recipient = out / "recipient"
        imported = ["--plan", str(recipient / "artifacts/plan.json"), "--policy", str(recipient / "artifacts/policy.yaml"),
                    "--manifest", str(manifest), "--directory", str(recipient / "work"), "--handoff", str(recipient / "bundle-index.json")]
        command("import", ["bundle", "import", "--from", str(bundle), "--directory", str(recipient), "--activate", "--claim-directory", str(claims)])
        verify_uncertain(recipient / "work", batch)
        imported_checkpoint = (recipient / "work/checkpoint.json").read_bytes()
        command("recipient-resume-blocked", ["run", *imported, "--side", side, "--revision", revision], (1,))
        require((recipient / "work/checkpoint.json").read_bytes() == imported_checkpoint, "rejected resume changed checkpoint")
        verify_uncertain(recipient / "work", batch)
        command("cleanup", ["cleanup", *imported, "--journal", "--faults", "--fault-history", "--execute"])
        command("completion", ["recover", *imported, "--inspect", "--verify-completion", "--verify-faults", "--check-cleanup"])
        completion = read(out / "completion.json")["completion"]
        require(completion["known_sessions_absent"] and completion["resource_evidence_complete"]
                and completion["fault_resources_verified"] and completion["fault_resources_absent"]
                and not completion["test_execution_complete"] and not completion["recovery_complete"],
                "cleanup forged execution completion or retained sessions")
        receipt = {"run_id": journal["run_id"], "workload_app_ids": {k: v["app_id"] for k, v in journal["workloads"].items()}}
        deadline = time.monotonic() + 180
        while True:
            observed = cleanup_probe(api, receipt, project, deadline)
            if all(observed["sessions_removed"].values()) and observed["live_instances"] == 0:
                break
            require(time.monotonic() < deadline, "independent session/VM cleanup did not converge")
            time.sleep(1)
        observed["scenario_cleanup_passed"] = observed.pop("passed")
        observed["sessions_and_instances_removed"] = True
        result.update(passed=True, run_id=journal["run_id"], cleanup=observed, completion=completion)
        return result
    except BaseException as error:
        result["reason"] = type(error).__name__ + ": " + str(error)
        raise
    finally:
        if coordinator is not None and coordinator.poll() is None:
            coordinator.kill()
            coordinator.wait(timeout=15)
        if worker_fd is not None:
            try:
                signal.pidfd_send_signal(worker_fd, signal.SIGKILL)
            except ProcessLookupError:
                pass
            os.close(worker_fd)
        if not result["passed"]:
            # Retain the failed drill and use scoped journal cleanup only. A failed
            # transfer/claim is never bypassed by deleting files or guessing IDs.
            recovery = source
            if recipient is not None and (recipient / "bundle-index.json").exists():
                recovery = imported
            try:
                if recovery is source:
                    command("failure-recover-lock", ["recover-lock", "--plan", str(plan), "--directory", str(directory), "--execute"])
                command("failure-cleanup", ["cleanup", *recovery, "--journal", "--faults", "--fault-history", "--execute"])
            except (ValueError, OSError, subprocess.SubprocessError):
                result["cleanup_pending"] = True
                try:
                    # A fault-API outage must not prevent scoped VM/session
                    # cleanup. Registration/consumer uncertainty remains explicit.
                    command("failure-session-cleanup", ["cleanup", *recovery, "--batch", batch, "--journal", "--execute"])
                except (ValueError, OSError, subprocess.SubprocessError):
                    result["session_cleanup_pending"] = True
        (out / "verdict.json").write_text(json.dumps(result, indent=2) + "\n")
