#!/usr/bin/env python3
"""Native resilience acceptance: checkout batches, pending recovery, API cleanup."""
import argparse
import contextlib
import tempfile
import ipaddress
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import re
import selectors
import signal
import stat
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import xml.etree.ElementTree as ET

MODES = ("success", "failure", "cancel")
STRICT_CASES = ("req-bounded", "req-outlier", "req-no-id", "req-no-ack")
TRANSPORT_CASES = ("tcp-seq", "pool-full", "pool-queue", "req-timeout", "req-body", "conn-gauge", "tcp-cancel")
CASES = (*MODES, "batch-success", "batch-failure", "batch-cancel", "pending-healthy", "pending-stuck", "flight-before", "flight-pending", "flight-after", "dep-pending", "dep-after", "auth-stall", "commit-loss", "commit-request", "commit-response", "bandwidth", "read-cut", "ssl-request", "retry-budget", "circuit-breaker", "bulkhead", "combined", "repeated", "isolation", *STRICT_CASES, *TRANSPORT_CASES, "tcp-flight")
RESTART_MATRIX = {
    "flight-before": ("before-acceptance", "write-before-acceptance", "http-restart-before"),
    "flight-pending": ("pending", "write-pending", "http-restart-pending"),
    "flight-after": ("committed", "write-committed", "http-restart-after"),
}
DEPENDENCY_MATRIX = {
    "dep-pending": ("pending", "dependency-pending", "http-dependency-pending"),
    "dep-after": ("committed", "dependency-committed", "http-dependency-after"),
}
BATCH_STEPS = ("baseline", "concurrent-outage", "restart-checkout", "restore-payment", "recover-customers")
STEPS = ("baseline", "payment-outage", "restart-checkout", "restore-payment", "recovery")
HEX32 = re.compile(r"[0-9a-f]{32}\Z")
SHA = re.compile(r"[0-9a-f]{40}\Z")


def require(condition, reason):
    if not condition:
        raise ValueError(reason)


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class API:
    def __init__(self, url, token):
        self.url, self.token = url.rstrip("/"), token
        self.opener = urllib.request.build_opener(NoRedirect())

    def get(self, path, timeout=10):
        request = urllib.request.Request(self.url + path, headers={"Authorization": "Bearer " + self.token})
        try:
            with self.opener.open(request, timeout=timeout) as response:
                body = response.read(1024 * 1024 + 1)
                require(len(body) <= 1024 * 1024, "API response exceeded evidence limit")
                return response.status, json.loads(body)
        except urllib.error.HTTPError as error:
            return error.code, None  # Never print response bodies or credentials.


def runner_configuration():
    require(platform.system() == "Linux" and platform.machine() == "x86_64", "requires native x86_64 Linux")
    require(Path("/dev/kvm").is_char_device() and os.access("/dev/kvm", os.R_OK | os.W_OK), "requires accessible /dev/kvm on the designated runner")
    marker = Path("/etc/faas/scenario-restart-acceptance-host")
    stamp = Path("/etc/faas/scenario-restart-acceptance-platform-sha")
    for path in (marker, stamp):
        metadata = path.lstat()
        require(stat.S_ISREG(metadata.st_mode) and metadata.st_uid == 0 and not metadata.st_mode & 0o022,
                "acceptance host marker and release stamp must be root-owned regular files without group/other write access")
    installed = stamp.read_text().strip()
    require(SHA.fullmatch(installed) is not None, "invalid installed platform release stamp")
    for key in ("FAAS_API", "FAAS_TOKEN", "FAAS_SCENARIO_ACCEPTANCE_ACCOUNT"):
        require(bool(os.environ.get(key)), "missing " + key)
    url = urllib.parse.urlsplit(os.environ["FAAS_API"])
    require(url.scheme == "https" and url.hostname and not url.username and not url.password and
            url.path in ("", "/") and not url.query and not url.fragment, "FAAS_API must be the dedicated public HTTPS API origin")
    account = os.environ["FAAS_SCENARIO_ACCEPTANCE_ACCOUNT"]
    require(str(uuid.UUID(account)) == account, "acceptance account ID must be a canonical UUID")
    require(bool(os.environ.get("GREGALE_DEPENDENCY_DATABASE_URL")) and os.environ.get("GREGALE_DEPENDENCY_DATABASE_DISPOSABLE") == "1",
            "dependency qualification requires a migrated disposable Postgres runtime connection and explicit opt-in")
    database = urllib.parse.urlsplit(os.environ["GREGALE_DEPENDENCY_DATABASE_URL"])
    require(database.scheme in ("postgres", "postgresql") and database.hostname and database.hostname != "localhost"
            and urllib.parse.parse_qs(database.query).get("sslmode") in (["require"], ["verify-full"]), "native auth requires a verified-TLS external Postgres DNS target")
    try:
        ipaddress.ip_address(database.hostname)
    except ValueError:
        pass
    else:
        raise ValueError("native auth requires a DNS target reachable under guest egress policy")
    api = API(os.environ["FAAS_API"], os.environ["FAAS_TOKEN"])
    status, who = api.get("/v1/account")
    require(status == 200 and isinstance(who, dict) and who.get("id") == account, "token does not identify the configured dedicated acceptance account")
    return api, {"installed_platform_sha": installed, "account_id": account, "api_origin": os.environ["FAAS_API"], "native_runner": True}


def case_contract(case):
    require(case in CASES, "unknown native acceptance case")
    if case == "tcp-flight":
        return "pool-cancel", "pool-cancel", "cancel", ("baseline", "cancel-active")
    if case in TRANSPORT_CASES:
        suite = "tcp-campaign" if case == "tcp-seq" else "pool-exhaustion" if case in ("pool-full", "pool-queue", "conn-gauge", "tcp-cancel") else "request-deadlines"
        mode = "strict-negative" if case in ("pool-queue", "req-body", "conn-gauge") else "cancel" if case == "tcp-cancel" else "success"
        sequence = ("baseline", "fault-campaign")
        return suite, suite, mode, sequence if mode == "strict-negative" else (*sequence, "await-cancellation") if mode == "cancel" else (*sequence, "restored")
    if case in STRICT_CASES:
        return "per-request-attempts", "per-request-attempts", "success" if case == "req-bounded" else "strict-negative", ("baseline", "verify-each-request", "restored") if case == "req-bounded" else ("baseline", "verify-each-request")
    if case in ("commit-request", "commit-response"):
        direction = case.removeprefix("commit-")
        return "http-commit-" + direction, "commit-" + direction + "-deadline", "success", ()
    if case == "isolation":
        return "http-isolation", "dependency-isolation", "success", ()
    if case == "repeated":
        return "http-repeated", "repeated-outage-recovery", "success", ()
    if case == "combined":
        return "http-combined", "combined-resilience", "success", ()
    if case == "bulkhead":
        return "http-bulkhead", "bulkhead-recovery", "success", ()
    if case == "circuit-breaker":
        return "http-circuit-breaker", "circuit-breaker-recovery", "success", ()
    if case == "retry-budget":
        return "http-retry-budget", "shared-retry-budget", "success", ()
    if case == "ssl-request":
        return "http-ssl-request", "connection-establishment-stall", "success", ()
    if case == "read-cut":
        return "http-read-cut", "read-interruption", "success", ()
    if case == "bandwidth":
        return "http-bandwidth", "bandwidth-restriction", "success", ()
    if case == "commit-loss":
        return "http-commit-loss", "commit-acknowledgement-loss", "success", ()
    if case == "auth-stall":
        return "http-auth-stall", "authentication-stall", "success", ()
    if case in DEPENDENCY_MATRIX:
        return "http-dependency-restart", DEPENDENCY_MATRIX[case][1], "success", ("baseline", "replace-payment", "reconcile-write")
    if case in RESTART_MATRIX:
        return "http-restart-matrix", RESTART_MATRIX[case][1], "success", ("baseline", "replace-caller", "reconcile-write")
    if case.startswith("pending-"):
        return "pending-recovery", "pending-recovery", "success" if case == "pending-healthy" else "stuck", ("probe-baseline", "recovery-probes", "dependency-restored")
    if case.startswith("batch-"):
        return "checkout-batch", "checkout-batch", case.removeprefix("batch-"), BATCH_STEPS
    return "checkout", "checkout-outage", case, STEPS


def manifests(repo, destination, prefix):
    require(re.fullmatch(r"[a-z][a-z0-9-]{2,19}", prefix) is not None, "acceptance project prefix must be a 3..20 character app slug")
    source = repo / "examples/scenario-checkout"
    result = {}
    for case in CASES:
        suite, _, mode, _ = case_contract(case)
        if case == "tcp-flight":
            text = (source / "gregale-pool-cancel-test.yaml").read_text()
            text = text.replace("project: pool-cancel", "project: " + prefix + "-" + case).replace("from: pool-cancel", "from: " + prefix + "-" + case)
            text = text.replace("source: ./native-probe", "source: " + json.dumps(str(source / "native-probe")))
        elif case in TRANSPORT_CASES:
            text = (source / ("gregale-" + suite + "-test.yaml")).read_text()
            require(text.count("project: " + suite) == 1 and text.count("source: ./native-probe") == 2, "transport fixture contract changed")
            text = text.replace("project: " + suite, "project: " + prefix + "-" + case)
            text = text.replace("from: " + suite, "from: " + prefix + "-" + case)
            text = text.replace("source: ./native-probe", "source: " + json.dumps(str(source / "native-probe")))
            if case == "pool-queue":
                require(text.count("max_queue: 2") == 1, "pool queue contract changed")
                text = text.replace("max_queue: 2", "max_queue: 1")
            elif case == "req-body":
                require(text.count("expect: {status: 504") == 1, "deadline contract changed")
                text = text.replace("expect: {status: 504", "expect: {status: 503")
            elif case == "conn-gauge":
                text = text.replace("connection_leaks: {path: /connections", "connection_leaks: {path: /")
            elif case == "tcp-cancel":
                text = text[:text.index("      - name: restored\n")] + "      - name: await-cancellation\n        command: " + json.dumps(["node", str(source / "test/native-cancel.mjs")]) + "\n"
        elif case in STRICT_CASES:
            write_source = repo / "examples/scenario-http-write"
            text = (write_source / "gregale-per-request-attempts-test.yaml").read_text()
            text = text.replace("project: per-request-attempts", "project: " + prefix + "-" + case)
            text = text.replace("from: per-request-attempts", "from: " + prefix + "-" + case)
            text = text.replace("GREGALE_RETRY_BUDGET_FIXTURE: '1'", "GREGALE_RETRY_BUDGET_FIXTURE: '1'\n      GREGALE_REQUEST_ATTEMPTS_MODE: '" + {"req-no-id": "missing-id", "req-no-ack": "missing-receipt"}.get(case, case.removeprefix("req-")) + "'")
            text = text.replace("duration: 8s", "duration: 12s").replace("duration: 2s\n              assertions: [strict", "duration: 6s\n              assertions: [strict")
            for workload in ("native-retry-app", "payment-postgres"):
                text = text.replace("source: ./" + workload, "source: " + json.dumps(str(write_source / workload)))
        elif case in ("auth-stall", "commit-loss", "commit-request", "commit-response", "bandwidth", "read-cut", "ssl-request", "retry-budget", "circuit-breaker", "bulkhead", "combined", "repeated", "isolation"):
            commit = case.startswith("commit-")
            write_source = repo / "examples/scenario-http-write"
            filename = "gregale-native-commit-test.yaml" if case == "commit-loss" else "gregale-native-" + case + "-test.yaml" if commit else "gregale-native-" + case + "-test.yaml" if case in ("bandwidth", "read-cut", "ssl-request", "retry-budget", "circuit-breaker", "bulkhead", "combined", "repeated", "isolation") else "gregale-native-auth-test.yaml"
            text = (write_source / filename).read_text()
            original = "project: http-native-commit" if case == "commit-loss" else "project: http-native-" + case.removeprefix("commit-") if commit else "project: http-native-" + case if case in ("bandwidth", "read-cut", "ssl-request", "retry-budget", "circuit-breaker", "bulkhead", "combined", "repeated", "isolation") else "project: http-native-auth"
            require(text.count(original) == 1, "native protocol fixture contract changed")
            text = text.replace(original, "project: " + prefix + "-" + case)
            for workload in ("app" if commit else "native-bulkhead-app" if case in ("bulkhead", "combined", "repeated", "isolation") else "native-breaker-app" if case == "circuit-breaker" else "native-retry-app" if case == "retry-budget" else "native-auth-app", "payment-postgres"):
                text = text.replace("source: ./" + workload, "source: " + json.dumps(str(write_source / workload)))
        elif suite in ("http-restart-matrix", "http-dependency-restart"):
            write_source = repo / "examples/scenario-http-write"
            dependency = suite == "http-dependency-restart"
            matrix = DEPENDENCY_MATRIX if dependency else RESTART_MATRIX
            template = (write_source / ("gregale-dependency-restart-test.yaml" if dependency else "gregale-restart-matrix-test.yaml")).read_text()
            stage, scenario, original_project = matrix[case]
            _, separator, body = template.partition("\nscenarios:\n")
            require(separator, "restart matrix omitted its scenario section")
            blocks = list(re.finditer(r"^  ([a-z-]+):$", body, re.MULTILINE))
            require([block.group(1) for block in blocks] == [value[1] for value in matrix.values()], "restart matrix checkpoint order or members changed")
            index = [block.group(1) for block in blocks].index(scenario)
            end = blocks[index + 1].start() if index + 1 < len(blocks) else len(body)
            text = ("version: 1\nsuites:\n  " + suite + ":\n    scenarios:\n"
                    "      - {scenario: " + scenario + ", engine: real-vm, profile: warm}\nscenarios:\n" + body[blocks[index].start():end])
            for old in ("project: " + original_project, "source: ./app", "source: ./" + ("payment-postgres" if dependency else "payment"), "HOLD_STAGE: " + stage):
                require(text.count(old) == 1, "restart matrix fixture contract changed")
            text = text.replace("project: " + original_project, "project: " + prefix + "-" + case)
            for workload in ("app", "payment-postgres" if dependency else "payment"):
                text = text.replace("source: ./" + workload, "source: " + json.dumps(str(write_source / workload)))
        elif suite == "pending-recovery":
            template = (source / "gregale-pending-test.yaml").read_text()
            require(template.count("project: pending-recovery-example") == 1 and template.count("source: ./native-probe") == 2 and template.count('PROBE_DELAY_MS: "25"') == 1,
                    "pending manifest contract changed; update the native acceptance variants")
            text = template.replace("project: pending-recovery-example", "project: " + prefix + "-" + case)
            text = text.replace("source: ./native-probe", "source: " + json.dumps(str(source / "native-probe")))
            if mode == "stuck":
                text = text.replace('PROBE_DELAY_MS: "25"', 'PROBE_DELAY_MS: "15000"')
        else:
            batch = suite == "checkout-batch"
            template = (source / ("gregale-batch-test.yaml" if batch else "gregale-test.yaml")).read_text()
            original_project = "batch-checkout-example" if batch else "checkout-resilience-example"
            for old in ("project: " + original_project, "source: ./app", "source: ./payment", "      - name: restart-checkout\n"):
                require(template.count(old) == 1, "checkout manifest contract changed; update the native acceptance variants")
            text = template.replace("project: " + original_project, "project: " + prefix + "-" + case)
            text = text.replace("source: ./app", "source: " + json.dumps(str(source / "app")))
            text = text.replace("source: ./payment", "source: " + json.dumps(str(source / "payment")))
            if mode != "success":
                name = "intentional-failure" if mode == "failure" else "await-cancellation"
                command = ["node", "-e", "process.exit(42)"] if mode == "failure" else ["node", "../test/native-cancel.mjs"]
                text = text.replace("      - name: restart-checkout\n", "      - name: " + name + "\n        command: " + json.dumps(command) + "\n      - name: restart-checkout\n")
        path = destination / (case + ".yaml")
        path.write_text(text)
        result[case] = (path, prefix + "-" + case)
    return result


def execute(command, log, marker=None, deadline_seconds=720, cleanup_seconds=240, environment=None, checkpoint=None):
    """Interrupt only after the nonce marker; drain logs while CLI cleans up."""
    environment = dict(os.environ if environment is None else environment)
    if marker:
        environment["GREGALE_NATIVE_CANCEL_MARKER"] = marker
    process = subprocess.Popen(command, env=environment, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ)
    pending = b""
    issued, timed_out = False, False
    deadline = time.monotonic() + deadline_seconds
    shutdown = None
    try:
        with Path(log).open("wb") as output:
            while selector.get_map() or process.poll() is None:
                now = time.monotonic()
                if now >= deadline and shutdown is None:
                    timed_out = True
                    process.send_signal(signal.SIGINT)
                    shutdown = now + cleanup_seconds
                if shutdown is not None and now >= shutdown:
                    if process.poll() is None:
                        process.kill()
                    raise ValueError("CLI exceeded independent cleanup deadline; native acceptance failed")
                for key, _ in selector.select(0.2):
                    chunk = os.read(key.fileobj.fileno(), 65536)
                    if not chunk:
                        selector.unregister(key.fileobj)
                        continue
                    output.write(chunk)
                    output.flush()
                    pending += chunk
                    while b"\n" in pending:
                        line, pending = pending.split(b"\n", 1)
                        if marker and not issued and line.strip() == marker.encode():
                            if checkpoint:
                                checkpoint()
                            process.send_signal(signal.SIGINT)
                            issued = True
                            shutdown = time.monotonic() + cleanup_seconds
                    require(len(pending) <= 1024 * 1024, "unterminated CLI log line exceeded limit")
        code = process.wait()
        require(not timed_out, "CLI scenario deadline exceeded")
        return code, issued
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGINT)
            cleanup_deadline = time.monotonic() + cleanup_seconds
            # Keep stdout open while cleanup runs. Closing it first could kill
            # Go on SIGPIPE, and waiting without draining could fill the pipe.
            with Path(log).open("ab") as output:
                while process.poll() is None and time.monotonic() < cleanup_deadline:
                    for key, _ in selector.select(0.2):
                        chunk = os.read(key.fileobj.fileno(), 65536)
                        if chunk:
                            output.write(chunk)
                        else:
                            selector.unregister(key.fileobj)
            if process.poll() is None:
                process.kill()
            process.wait()
        selector.close()
        process.stdout.close()


def verify_fault(chaos, minimum):
    matches = chaos.get("matches", [])
    require(chaos.get("rules_installed") == 1 and len(matches) == 1 and matches[0].get("min_matches") == minimum and matches[0].get("matches", 0) >= minimum and matches[0].get("rule", {}).get("kind") == "tcp_timeout",
            "dependency outage was not exercised with positive fault-match evidence")


def verify_http(evidence, names):
    require([item.get("name") for item in evidence] == list(names) and all(item.get("passed") is True and item.get("status") == 200 for item in evidence),
            "missing or failed named HTTP business assertions")


def verify_restart(restart, batch, workload="app"):
    require(restart.get("workload") == workload and restart.get("status") == "passed" and restart.get("processing_status") == "completed" and restart.get("boot_method") == "cold_boot", "restart was not durably completed as a cold replacement")
    require(str(uuid.UUID(restart.get("wake_id", ""))) == restart["wake_id"] and restart.get("readiness_attempts", 0) > 0, "restart receipt or readiness evidence missing")
    for field in ("storage_before", "storage_after", "readiness"):
        evidence = restart.get(field, [])
        if batch:
            names = ("batch-checkout-ready",) if field == "readiness" else ("preserved-customer-intents", *("preserved-customer-" + str(i) + "-pending" for i in range(6)))
            verify_http(evidence, names)
        else:
            require(len(evidence) == 1 and evidence[0].get("passed") is True, "restart " + field + " did not pass")


def verify_pending(step, mode):
    load = step.get("load", {})
    phases = load.get("chaos_phases", [])
    require([phase.get("name") for phase in phases] == ["baseline", "dependency-outage", "recovery"], "pending gate omitted a scheduled phase")
    verify_fault(phases[1].get("chaos", {}), 1)
    require(all(phase.get("actual_duration_ms", 0) > 0 and phase.get("started_at") and phase.get("finished_at") for phase in phases), "pending gate omitted executed phase timing")
    require({stream.get("name") for stream in load.get("streams", [])} == {"healthy", "pending"}, "pending gate omitted independent HTTP streams")
    require(load.get("thresholds") and all(check.get("passed") is True for check in load["thresholds"]), "unrelated load threshold failure cannot satisfy pending acceptance")
    recovery = phases[2].get("recovery", {})
    require(recovery.get("max_pending_age_ms") == 500 and recovery.get("within_ms") == 2000 and recovery.get("sustain_ms") == 2000 and recovery.get("window_ms") == 1000 and recovery.get("windows_checked", 0) > 1,
            "pending recovery budget or evaluated windows are missing")
    checks = recovery.get("thresholds", [])
    expected = {"p95_ms": ("<=", 1500), "error_rate": ("<=", 0), "min_samples": (">=", 3)}
    require(len(checks) == 3 and {check.get("metric") for check in checks} == set(expected), "completed recovery threshold evidence is missing")
    for check in checks:
        operator, limit = expected[check["metric"]]
        actual = check.get("actual")
        require(check.get("passed") is True and check.get("operator") == operator and check.get("limit") == limit and isinstance(actual, (int, float)) and not isinstance(actual, bool) and math.isfinite(actual) and actual >= 0 and (actual <= limit if operator == "<=" else actual >= limit),
                "completed healthy traffic must meet every recovery threshold")
    require(phases[2].get("requests", 0) >= 3, "recovery healthy traffic was not exercised")
    if mode == "success":
        require(step.get("status") == "passed" and load.get("status") == "passed" and recovery.get("passed") is True and recovery.get("overdue_requests") == 0 and not recovery.get("error") and isinstance(recovery.get("recovery_ms"), (int, float)) and 0 <= recovery["recovery_ms"] <= 2000,
                "healthy pending-probe control did not recover")
    else:
        require(step.get("status") == "failed" and load.get("status") == "failed" and recovery.get("passed") is False and recovery.get("overdue_requests", 0) >= 1 and "max_pending_age" in recovery.get("error", "") and "recovery failed" in step.get("error", "") and "max_pending_age" in step.get("error", ""),
                "stuck request did not cause the expected pending-age recovery failure")


def cleanup_identity(document, project=None):
    require(isinstance(document, list) and len(document) == 1, "expected exactly one executed scenario receipt")
    report = document[0]
    require(HEX32.fullmatch(report.get("run_id", "")) is not None and report.get("app_slug"), "receipt omitted workload identity")
    ids = report.get("workload_app_ids", {})
    services = {"payment", "inventory"} if report.get("scenario") == "dependency-isolation" else {"payment"}
    require(isinstance(ids, dict) and len(ids) == len(services)+1 and services <= set(ids) and len(set(ids.values())) == len(ids), "all created workload IDs are required for cleanup inspection")
    for value in ids.values():
        require(str(uuid.UUID(value)) == value, "invalid workload app ID")
    if project:
        require(set(ids) == {project, *services}, "workload identities do not match the generated acceptance project")
    return report


def verify_report(document, case, exit_code, cancelled, project=None):
    suite, scenario, mode, sequence = case_contract(case)
    report = cleanup_identity(document, project)
    require(report.get("scenario") == scenario and report.get("engine") == "real-vm" and report.get("profile") == "warm", "wrong scenario, engine or initial profile")
    require(re.fullmatch(r"[0-9a-f]{64}", report.get("source_sha256", "")) is not None, "receipt omitted deployed app source digest")
    require(not report.get("cleanup_error") and not report.get("skip_reason"), "cleanup failed or prerequisites skipped")
    cleanup = [phase for phase in report.get("phases", []) if phase.get("name") == "cleanup"]
    require(len(cleanup) == 1 and cleanup[0].get("status") == "passed", "missing successful cleanup phase")
    steps = report.get("steps", [])
    expected = sequence if case == "tcp-flight" or case in TRANSPORT_CASES or mode == "strict-negative" else sequence[:2] if mode == "stuck" else sequence if mode == "success" else (*sequence[:2], "intentional-failure" if mode == "failure" else "await-cancellation")
    require([step.get("name") for step in steps] == list(expected), "missing or unexpected acceptance stages")
    if mode == "success":
        require(exit_code == 0 and report.get("status") == "passed" and not report.get("error") and not cancelled, "success run failed")
    else:
        require(exit_code > 0 and report.get("status") == "failed" and steps[-1].get("status") == "failed", "expected negative path did not execute")
    if case == "tcp-flight":
        require(cancelled and "context canceled" in steps[-1].get("error", ""), "active cancellation not issued")
        verify_http(steps[0].get("requests", []), ("ready",))
        verify_fault(steps[1].get("chaos", {}), 2)
        return report
    if case in TRANSPORT_CASES:
        require(cancelled == (mode == "cancel"), "transport cancellation mismatch")
        verify_transport_report(report, case)
        return report
    if case in STRICT_CASES:
        require(not cancelled, "strict negative run was cancelled")
        verify_strict_request_report(report, case)
        return report
    if case in ("auth-stall", "commit-loss", "commit-request", "commit-response", "bandwidth", "read-cut", "ssl-request", "retry-budget", "circuit-breaker", "bulkhead", "combined", "repeated", "isolation"):
        names = ("a-baseline", "b-baseline") if case == "isolation" else ("caller-baseline" if case.startswith("commit-") else "query-baseline",)
        verify_http(report.get("requests", []), names)
        for service in (("payment", "inventory") if case == "isolation" else ("payment",)):
            hot = report.get("service_hot", {}).get(service, {})
            require(hot.get("no_new_wake") is True and not hot.get("planned_restarts"), "native provider unexpectedly woke or restarted")
        return report
    if suite in ("http-restart-matrix", "http-dependency-restart"):
        dependency = suite == "http-dependency-restart"
        matrix = DEPENDENCY_MATRIX if dependency else RESTART_MATRIX
        require(all(step.get("status") == "passed" for step in steps), "in-flight scenario stage failed")
        verify_http(steps[0].get("requests", []), ("seed-charge", "seed-ledger"))
        restart = steps[1].get("restart", {})
        verify_restart(restart, False, "payment" if dependency else "app")
        for field, names in (("storage_before", ("retained-ledger",)), ("storage_after", ("retained-ledger",)), ("readiness", ("replacement-payment-ready" if dependency else "replacement-idle",))):
            verify_http(restart.get(field, []), names)
        flight = restart.get("in_flight", {})
        require(flight.get("name") == "interrupted-write" and flight.get("checkpoint_attempts", 0) > 0
                and flight.get("pending_at_restart") is True and flight.get("restart_completed") is True and flight.get("joined") is True
                and flight.get("outcome") in ("request_ended", "canceled_after_restart"), "in-flight request lifecycle is unproven")
        verify_http(flight.get("checkpoints", []), ("caller-pending", "provider-" + matrix[case][0]))
        verify_http(steps[2].get("requests", []), ("reconciled-ledger",))
        if dependency:
            hot = report.get("service_hot", {}).get("payment", {})
            require(hot.get("no_new_wake") is True and hot.get("planned_restarts") == [restart["wake_id"]], "service restart hid an unexpected wake or omitted its planned receipt")
        return report
    if suite == "pending-recovery":
        require(not cancelled, "pending gate was interrupted rather than evaluated")
        require(steps[0].get("status") == "passed", "probe baseline failed")
        verify_http(steps[0].get("requests", []), ("probe-baseline-ready",))
        verify_pending(steps[1], mode)
        if mode == "success":
            require(steps[2].get("status") == "passed", "dependency did not recover after fault clear")
            verify_http(steps[2].get("requests", []), ("dependency-reachable-again",))
        return report
    batch = suite == "checkout-batch"
    require(all(step.get("status") == "passed" for step in steps[:2]), "baseline or charged-outage assertion failed")
    verify_fault(steps[1].get("chaos", {}), 6 if batch else 1)
    if batch:
        verify_http(steps[0].get("requests", []), ("ledger-before-batch",))
        verify_http(steps[1].get("requests", []), ("all-customer-intents-persisted", *("customer-" + str(i) + "-pending" for i in range(6))))
    if mode == "success":
        require(all(step.get("status") == "passed" for step in steps), "restart or recovery failed")
        verify_restart(steps[2].get("restart", {}), batch)
        require(steps[3].get("fault_cleared") is True, "fault was not explicitly cleared")
        if batch:
            verify_http(steps[4].get("requests", []), ("every-order-confirmed", *("customer-" + str(i) + "-confirmed" for i in range(6))))
        else:
            for index in (0, 4):
                require(steps[index].get("load", {}).get("status") == "passed", "baseline or recovered business journey load failed")
    elif mode == "failure":
        require("exit status 42" in steps[-1].get("error", "") and not cancelled, "wrong intentional failure")
    else:
        require(cancelled and "context canceled" in steps[-1].get("error", ""), "cancellation was not observed at the active-fault boundary")
    return report


def verify_strict_request_report(report, case):
    steps = report["steps"]
    require(steps[0].get("status") == "passed", "strict baseline failed")
    verify_http(steps[0].get("requests", []), ("ready",))
    phases = steps[1].get("load", {}).get("chaos_phases", [])
    require([phase.get("name") for phase in phases] == ["baseline", "fault", "recovery"], "strict phases missing")
    require(phases[2].get("step_recovery", {}).get("dependency-read", {}).get("passed") is True, "strict dependency recovery missing")
    hot = report.get("service_hot", {}).get("payment", {})
    require(hot.get("no_new_wake") is True and not hot.get("planned_restarts"), "strict provider unexpectedly restarted")
    fault = phases[1]
    verify_fault(fault.get("chaos", {}), 1)
    attempt = fault.get("retry_amplification", {})
    strict = attempt.get("per_request", {})
    require(attempt.get("request") == "dependency-read" and attempt.get("max_attempts_per_request") == 2 and strict.get("limit") == 2, "strict budget missing")
    require(type(strict.get("requests")) is int and strict["requests"] >= 10 and strict.get("overflow") == 0 and strict.get("capacity") == 1024, "strict sample incomplete")
    average = attempt.get("attempts_per_request")
    require(type(average) in (int, float) and math.isfinite(average) and 0 < average <= 2 and type(attempt.get("attempts")) is int and attempt["attempts"] > 0, "average must pass with actual dependency calls")
    if case == "req-bounded":
        require(strict.get("passed") is True and strict.get("correlated") == strict["requests"] and strict.get("max_attempts", 3) <= 2 and strict.get("uncorrelated_attempts") == 0, "bounded request proof failed")
        verify_http(steps[2].get("requests", []), ("confirmed",))
    else:
        require(strict.get("passed") is False and attempt.get("passed") is False, "negative strict assertion did not fail")
        if case == "req-outlier":
            require(strict.get("max_attempts") == 11 and strict.get("violation_count") == 1 and strict.get("uncorrelated_attempts") == 0 and strict.get("correlated") == strict["requests"], "hidden outlier proof missing")
        elif case == "req-no-id":
            require(strict.get("uncorrelated_attempts", 0) > 0 and "correlation IDs" in strict.get("error", ""), "missing propagation proof missing")
        else:
            require(strict.get("uncorrelated_attempts") == 0 and "completion receipt missing" in strict.get("error", ""), "missing receipt proof missing")


def require_active_pool(snapshot):
    for field, expected in (("capacity", 2), ("active", 2), ("queued", 2), ("active_connections", 2), ("queue_timers", 2), ("deadline_timers", 2), ("queue_deadline_ms", 10000), ("attempt_deadline_ms", 30000)):
        require(type(snapshot.get(field)) is int and snapshot[field] == expected, "active pool checkpoint missing " + field)


def confirm_pool_checkpoint(path, marker):
    require(path.stat().st_size <= 65536, "pool cancellation proof exceeded limit")
    proof = json.loads(path.read_text())
    require(proof.get("marker") == marker and HEX32.fullmatch(proof.get("namespace", "")), "cancellation checkpoint identity differs")
    url = urllib.parse.urlsplit(proof.get("app_url", ""))
    require(url.scheme == "http" and url.hostname == "127.0.0.1" and url.port and url.path == "" and not url.username and not url.password and not url.query and not url.fragment, "checkpoint must use the local CLI proxy")
    with urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect()).open(proof["app_url"] + "/pool-status", timeout=2) as response:
        body = response.read(4097)
        require(response.status == 200 and len(body) <= 4096, "invalid live cancellation snapshot")
        snapshot = json.loads(body)
    require_active_pool(snapshot)
    require(snapshot.get("namespace") == proof["namespace"] and snapshot.get("instance_id") == proof["baseline"].get("instance_id"), "live checkpoint identity differs")
    proof["checkpoint"] = snapshot
    temporary = path.with_suffix(".checkpoint.tmp")
    with temporary.open("x") as output:
        os.chmod(temporary, 0o600)
        output.write(json.dumps(proof) + "\n")
    temporary.replace(path)


def verify_pool_cancel_proof(proof, report, marker):
    require(proof.get("namespace") == report["run_id"] and proof.get("marker") == marker, "pool proof run or nonce differs")
    baseline = proof.get("baseline", {})
    identity = baseline.get("instance_id", "")
    require(str(uuid.UUID(identity)) == identity and uuid.UUID(identity).int != 0, "pool proof instance missing")
    require(baseline.get("namespace") == report["run_id"] and type(baseline.get("waiters_created")) is int and type(baseline.get("waiters_finished")) is int and baseline["waiters_created"] == baseline["waiters_finished"], "pool baseline identity or waiter accounting differs")
    for stage in ("observed", "checkpoint"):
        snapshot = proof.get(stage, {})
        require_active_pool(snapshot)
        require(snapshot.get("namespace") == report["run_id"] and snapshot.get("instance_id") == identity, "pool checkpoint reset or namespace differs")
    clean = proof.get("cleanup", {})
    require(clean.get("namespace") == report["run_id"] and clean.get("instance_id") == identity, "pool cleanup identity differs")
    for field in ("active", "queued", "active_connections", "queue_timers", "deadline_timers"):
        require(type(baseline.get(field)) is int and baseline[field] == 0 and type(clean.get(field)) is int and clean[field] == 0, "pool resources did not join: " + field)
    for field, delta in (("queued_request_cancellations", 2), ("cancelled_requests", 4)):
        require(type(clean.get(field)) is int and type(baseline.get(field)) is int and clean[field] - baseline[field] == delta, "request cancellation not observed")
    require(type(clean.get("waiters_created")) is int and type(clean.get("waiters_finished")) is int and clean["waiters_created"] == clean["waiters_finished"] and clean["waiters_created"] - baseline.get("waiters_created", clean["waiters_created"]) == 2, "waiter retirement incomplete")
    require(type(clean.get("waiters_cancelled")) is int and type(baseline.get("waiters_cancelled")) is int and 0 <= clean["waiters_cancelled"] - baseline["waiters_cancelled"] <= 2, "queued waiter cancellation accounting invalid")
    for field in ("waiters_timed_out", "application_timeouts"):
        require(type(baseline.get(field)) is int and type(clean.get(field)) is int and clean[field] == baseline[field], "natural timeout cannot qualify cancellation")
    require(type(proof.get("cleanup_elapsed_ms")) is int and 0 <= proof["cleanup_elapsed_ms"] < 5000, "resource cleanup deadline missing")
    return {"passed": True, "instance_id": identity, "requests_cancelled": clean["cancelled_requests"] - baseline["cancelled_requests"], "waiters_cancelled": clean["waiters_cancelled"] - baseline["waiters_cancelled"], "queued_requests_cancelled": clean["queued_request_cancellations"] - baseline["queued_request_cancellations"], "waiters_retired": 2}


def verify_transport_report(report, case):
    steps = report["steps"]
    require(steps[0].get("status") == "passed", "transport baseline failed")
    verify_http(steps[0].get("requests", []), ("ready",))
    load = steps[1].get("load", {})
    phases = load.get("chaos_phases", [])
    cycles = 4 if case == "tcp-seq" else 1
    require(len(phases) == 1 + 2 * cycles, "transport phases incomplete")
    require(all(p.get("actual_duration_ms", 0) > 0 and p.get("started_at") and p.get("finished_at") for p in phases), "transport phase timing missing")
    require({stream.get("name") for stream in load.get("streams", [])} == {"dependency", "health"}, "transport streams missing")
    require(load.get("thresholds") and all(c.get("passed") is True for c in load["thresholds"]), "unrelated load failure")
    hot = report.get("service_hot", {}).get("payment", {})
    require(hot.get("no_new_wake") is True and not hot.get("planned_restarts"), "transport provider restarted")
    for phase in phases:
        health = [step for step in phase.get("steps", []) if step.get("name") == "health"]
        require(len(health) == 1 and health[0].get("requests", 0) >= 3 and health[0].get("thresholds") and all(c.get("passed") is True for c in health[0]["thresholds"]), "independent health assertion missing")
    kinds = ("tcp_latency", "tcp_bandwidth", "tcp_timeout", "tcp_reset") if cycles == 4 else ("tcp_timeout",)
    for index, kind in enumerate(kinds):
        fault, recovery = phases[1 + index * 2:3 + index * 2]
        chaos = fault.get("chaos", {})
        matches = chaos.get("matches", [])
        require(chaos.get("rules_installed") == 1 and len(matches) == 1 and matches[0].get("min_matches") == 1 and matches[0].get("matches", 0) >= 1, "transport fault was not exercised")
        require([m.get("rule", {}).get("kind") for m in fault["chaos"]["matches"]] == [kind], "wrong transport fault order")
        attempt = fault.get("connection_attempts", {})
        require(attempt.get("passed") is True and attempt.get("request") == "dependency-probe" and attempt.get("max_attempts_per_request") == 2 and attempt.get("attempts", 0) > 0, "TCP attempt proof missing")
        require(recovery.get("step_recovery", {}).get("dependency-probe", {}).get("passed") is True, "transport recovery missing")
    leaks = load.get("connection_leaks", {})
    require(leaks.get("source") == "application" and leaks.get("samples", 0) >= 3 and leaks.get("path") == ("/" if case == "conn-gauge" else "/connections"), "connection observations missing")
    if case == "conn-gauge":
        require(leaks.get("passed") is False and leaks.get("baseline") is None and "connection baseline unavailable" in leaks.get("error", ""), "missing gauge did not fail closed")
    else:
        require(leaks.get("passed") is True and type(leaks.get("baseline")) is int and leaks["baseline"] == 0 and str(uuid.UUID(leaks.get("instance_id", ""))) == leaks["instance_id"], "connection baseline missing")
        drain = leaks.get("post_drain", {})
        require(drain.get("passed") is True and drain.get("remaining") == 0 and drain.get("limit") == 0 and drain.get("samples", 0) >= 3, "socket drain missing")
    if case in ("pool-full", "pool-queue", "conn-gauge", "tcp-cancel"):
        pool = load.get("pool_exhaustion", {})
        require(pool.get("source") == "application" and pool.get("peak_active") == 2 and pool.get("peak_queued") == 2 and type(pool.get("max_wait_ms")) is int and 0 < pool["max_wait_ms"] <= 300, "pool exhaustion not observed")
        budget = pool.get("budget", {})
        require(budget.get("capacity") == 2 and budget.get("max_queue") == (1 if case == "pool-queue" else 2), "pool budget missing")
        require(pool.get("passed") is (case != "pool-queue"), "wrong pool assertion verdict")
        if case == "pool-queue":
            require(pool.get("peak_queued", 0) > budget["max_queue"] and "pool capacity, queue depth or wait budget exceeded" in pool.get("error", ""), "queue violation missing")
        drain = pool.get("post_drain", {})
        snap = drain.get("snapshot", {})
        require(drain.get("passed") is (case != "pool-queue") and snap.get("active") == 0 and snap.get("queued") == 0 and snap.get("waiters_created") == snap.get("waiters_finished") and type(snap.get("waiters_created")) is int, "pool drain missing")
        if case != "pool-queue":
            require(phases[1].get("pool_exhaustion", {}).get("queued_requests", 0) >= 3 and phases[1]["pool_exhaustion"].get("passed") is True, "pool waiter samples missing")
            recovery_pool = phases[2].get("pool_exhaustion", {})
            require(recovery_pool.get("passed") is True and recovery_pool.get("windows") and all(w.get("passed") is True and w.get("required", 0) >= 5 and w.get("completed", 0) >= w["required"] for w in recovery_pool["windows"]), "pool completion recovery missing")
    if case in ("req-timeout", "req-body"):
        for index, phase in enumerate(phases):
            deadline = phase.get("request_deadlines", {}).get("dependency-probe", {})
            negative = case == "req-body" and index == 1
            require(deadline.get("within_ms") == 750 and deadline.get("min_samples") == 3 and deadline.get("requests", 0) >= 3, "deadline sample missing")
            require(type(deadline.get("max_ms")) in (int, float) and math.isfinite(deadline["max_ms"]) and 0 <= deadline["max_ms"] <= 750, "deadline maximum invalid")
            require(deadline.get("passed") is (not negative) and deadline.get("expected_status") == (503 if negative else 504 if index == 1 else 200), "deadline contract mismatch")
            require(deadline.get("failures", 0) > 0 if negative else deadline.get("failures") == 0, "deadline response proof missing")
    if case == "tcp-cancel":
        require(steps[1].get("status") == "passed" and "context canceled" in steps[-1].get("error", ""), "drained cancellation checkpoint missing")
    elif case not in ("pool-queue", "req-body", "conn-gauge"):
        verify_http(steps[-1].get("requests", []), ("dependency-restored",))


def verify_artifacts(directory, document, case):
    _, _, mode, _ = case_contract(case)
    html = directory / "results.html"
    xml = directory / "results.xml"
    require(html.is_file() and 0 < html.stat().st_size <= 16 * 1024 * 1024 and "<html" in html.read_text().lower(), "HTML evidence is missing or invalid")
    require(xml.is_file() and 0 < xml.stat().st_size <= 16 * 1024 * 1024, "JUnit evidence is missing or invalid")
    root = ET.parse(xml).getroot()
    cases = root.findall("testcase")
    require(root.get("tests") == "1" and len(cases) == 1 and cases[0].find("skipped") is None, "JUnit omitted or skipped the scenario")
    require((cases[0].find("failure") is None) == (mode == "success"), "JUnit result disagrees with the native case")
    require(json.loads(cases[0].findtext("system-out", "")) == document[0], "JUnit receipt differs from JSON evidence")


def cleanup_probe(api, report, project, deadline=None):
    if deadline is None:
        deadline = time.monotonic() + 180
    def get(path):
        remaining = deadline - time.monotonic()
        require(remaining > 0, "cleanup inspection exceeded its independent deadline")
        return api.get(path, timeout=min(10, remaining))
    run_id = report["run_id"]
    sessions = {}
    services = ("payment", "inventory") if report.get("scenario") == "dependency-isolation" else ("payment",)
    for name in (project, *(project + "-" + service for service in services)):
        status, _ = get("/v1/dev/sessions/" + name + "?" + urllib.parse.urlencode({"workspace_id": run_id}))
        require(status in (200, 404), "cleanup session probe returned an unexpected API status")
        sessions[name] = status == 404
    status, _ = get("/v1/dev/test-runs/" + run_id + "/chaos/matches")
    require(status in (200, 404), "cleanup namespace probe returned an unexpected API status")
    namespace_removed = status == 404
    owned_ids = set(report["workload_app_ids"].values())
    cursor, seen, live = "", set(), 0
    for _ in range(100):
        status, page = get("/v1/instances?" + urllib.parse.urlencode({"limit": 100, "before": cursor}))
        require(status == 200 and isinstance(page, dict) and isinstance(page.get("instances"), list), "invalid live-instance inventory response")
        for instance in page["instances"]:
            require(isinstance(instance, dict) and instance.get("app_id"), "instance inventory omitted ownership")
            if instance["app_id"] in owned_ids:
                live += 1
        cursor = page.get("next_before", "")
        if not cursor:
            break
        require(cursor not in seen, "instance inventory repeated a pagination cursor")
        seen.add(cursor)
    else:
        raise ValueError("instance inventory exceeded bounded pagination")
    return {"sessions_removed": sessions, "namespace_removed": namespace_removed, "live_instances": live,
            "passed": all(sessions.values()) and namespace_removed and live == 0}


def verify_cleanup(api, report, project, seconds=180):
    deadline = time.monotonic() + seconds
    while True:
        result = cleanup_probe(api, report, project, deadline)
        if result["passed"]:
            return result
        require(time.monotonic() < deadline, "cleanup did not converge: sessions, namespace or live instances remain")
        time.sleep(1)


def verify_dependency_database(repo, report):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/postgres-cleanup.mjs"), "check", report["run_id"]],
                            capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent Postgres ledger cleanup probe failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "remaining": 0}, "Postgres ledger namespace remains or probe identity differs")
    return proof


@contextlib.contextmanager
def auth_environment(case, run_dir):
    environment = dict(os.environ)
    if case == "tcp-flight":
        environment["GREGALE_NATIVE_POOL_CANCEL_PROOF"] = str((run_dir / "pool-cancel-proof.json").resolve())
    if case not in STRICT_CASES and case not in ("auth-stall", "commit-loss", "commit-request", "commit-response", "bandwidth", "read-cut", "ssl-request", "retry-budget", "circuit-breaker", "bulkhead", "combined", "repeated", "isolation"):
        yield environment
        return
    with tempfile.TemporaryDirectory(prefix="gregale-native-auth-") as directory:
        key, cert = Path(directory) / "key.pem", Path(directory) / "cert.pem"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=localhost",
                        "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1", "-keyout", str(key), "-out", str(cert)],
                       check=True, timeout=10, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        environment.update(GREGALE_NATIVE_AUTH_CERT_PEM=cert.read_text(), GREGALE_NATIVE_AUTH_KEY_PEM=key.read_text(),
                           GREGALE_NATIVE_AUTH_CONTROL_TOKEN=uuid.uuid4().hex + uuid.uuid4().hex,
                           GREGALE_NATIVE_AUTH_PROOF=str((run_dir / "auth-proof.json").resolve()))
        if case == "isolation":
            environment["GREGALE_NATIVE_ISOLATION_PROOF"] = str((run_dir / "isolation-proof.json").resolve())
        if case == "repeated":
            environment["GREGALE_NATIVE_REPEATED_PROOF"] = str((run_dir / "repeated-proof.json").resolve())
        if case == "combined":
            environment["GREGALE_NATIVE_COMBINED_PROOF"] = str((run_dir / "combined-proof.json").resolve())
        if case == "bulkhead":
            environment["GREGALE_NATIVE_BULKHEAD_PROOF"] = str((run_dir / "bulkhead-proof.json").resolve())
        if case == "circuit-breaker":
            environment["GREGALE_NATIVE_CIRCUIT_BREAKER_PROOF"] = str((run_dir / "circuit-breaker-proof.json").resolve())
        if case == "retry-budget":
            environment["GREGALE_NATIVE_RETRY_BUDGET_PROOF"] = str((run_dir / "retry-budget-proof.json").resolve())
        if case == "ssl-request":
            environment["GREGALE_NATIVE_SSL_REQUEST_PROOF"] = str((run_dir / "ssl-request-proof.json").resolve())
        if case == "read-cut":
            environment["GREGALE_NATIVE_READ_CUT_PROOF"] = str((run_dir / "read-cut-proof.json").resolve())
        if case == "bandwidth":
            environment["GREGALE_NATIVE_BANDWIDTH_PROOF"] = str((run_dir / "bandwidth-proof.json").resolve())
        if case.startswith("commit-"):
            environment["GREGALE_NATIVE_COMMIT_PROOF"] = str((run_dir / "commit-proof.json").resolve())
        yield environment


def verify_auth_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace, "native auth proof namespace mismatch")
    require(str(uuid.UUID(proof.get("instance_id", ""))) == proof["instance_id"], "native auth proxy identity missing")
    baseline_pids = proof.get("backend_baseline_pids")
    require(isinstance(baseline_pids, list) and len(baseline_pids) <= 128 and all(type(pid) is int and pid > 0 for pid in baseline_pids), "native auth backend baseline missing")
    baseline = proof.get("baseline", {})
    require(baseline.get("namespace") == namespace and baseline.get("instance_id") == proof["instance_id"] and baseline.get("attempts") == 1 and baseline.get("tls_connections_verified") == 1 and baseline.get("authentication_challenges_seen") == 1, "native auth baseline did not verify TLS/login")
    require(proof.get("outage", {}).get("responses") == [503, 503, 503], "three explicit failed connection attempts required")
    require(proof.get("recovery", {}).get("responses") == [200, 200] and proof["recovery"].get("value") == 42, "native auth query recovery missing")
    for stage, attempts in (("outage", 4), ("recovery", 6), ("cleanup", 6)):
        block = proof.get(stage, {})
        status = block.get("status", {})
        require(status.get("namespace") == namespace and status.get("instance_id") == proof["instance_id"], "native auth instance changed")
        require(status.get("attempts") == attempts and status.get("tls_connections_verified") == attempts and status.get("connection_stalls_fired") == 3, "native auth attempts or positive TLS fault proof missing")
        records = status.get("connection_stalls", [])
        require(len(records) == 3 and all(record.get("phase") == "authentication" and record.get("outcome") == "canceled" and record.get("configured_ms") == 5000
                and isinstance(record.get("elapsed_ms"), (float, int)) and 1000 <= record["elapsed_ms"] < 5000 for record in records), "native auth stall phase/deadline proof missing")
        require(all(status.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers")), "native auth connection resources remain")
        require(status.get("connection_stall_enabled") is (stage == "outage"), "native auth control was not cleared")
        require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native auth independent backend retirement proof missing")
    require(proof["cleanup"]["status"].get("stopped") is True, "native auth fixture was not stopped")
    return {"passed": True, "namespace": namespace, "faults": 3}


def verify_auth_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-auth-acceptance.mjs"), "check", report["run_id"], str(proof_path)],
                            capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native auth backend cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native auth backend remains or identity differs")
    return proof



def verify_commit_proof(proof, report, fault_mode="drop"):
    require(fault_mode in ("drop", "request-deadline", "response-deadline"), "unknown native COMMIT fault")
    deadline, request_held = fault_mode != "drop", fault_mode == "request-deadline"
    require(proof.get("fault_mode") == fault_mode, "native COMMIT fault mode mismatch")
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace, "native COMMIT proof namespace mismatch")
    for field in ("instance_id", "ledger_id"):
        require(str(uuid.UUID(proof.get(field, ""))) == proof[field], "native COMMIT identity missing")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native COMMIT backend baseline missing")
    unknown = {"status": 503, "body": {"attempts": 1, "statuses": [503], "body_errors": [], "error": "payment_unavailable", "payment_outcome": "unknown", "retryable": True}}
    if deadline:
        unknown["body"] = {key: unknown["body"][key] for key in ("error", "payment_outcome", "retryable")}
        block = proof.get("unknown", {})
        require(block.get("deadline_ms") == 1500 and isinstance(block.get("elapsed_ms"), (int, float)) and 1000 <= block["elapsed_ms"] < 3500, "native caller deadline missing or unbounded")
    replay = {"status": 200, "body": {"attempts": 1, "statuses": [201], "body_errors": [], "id": "ack-loss", "status": "confirmed"}}
    conflict = {"status": 409, "body": {"attempts": 1, "statuses": [409], "body_errors": [], "error": "idempotency_conflict"}}
    require(proof.get("unknown", {}).get("response") == unknown, "native COMMIT unknown outcome or no-retry proof missing")
    require(proof.get("replay", {}).get("response") == replay and proof["replay"].get("conflict") == conflict, "native COMMIT safe replay/conflict missing")
    for stage, attempts, commits in (("baseline", 0, 0), ("unknown", 1, 0 if request_held else 1), ("replay", 3, 1 if request_held else 2), ("cleanup", 3, 1 if request_held else 2)):
        block = proof.get(stage, {})
        status = block if stage == "baseline" else block.get("status", {})
        require(status.get("fault_mode") == fault_mode and status.get("namespace") == namespace and status.get("instance_id") == proof["instance_id"] and status.get("ledger_id") == proof["ledger_id"], "native COMMIT identity changed")
        require(status.get("attempts") == attempts and status.get("commits_forwarded") == commits and status.get("acknowledgements_dropped") == (0 if stage == "baseline" or deadline else 1), "native COMMIT attempts or acknowledgement fault mismatch")
        require(status.get("tls_connections_verified", 0) >= 1 and status.get("tls_failures") == 0 and status.get("invalid_frames") == 0, "native COMMIT verified TLS missing")
        require(all(status.get(key) == 0 for key in ("active_writes", "queued_writes", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_faults", "fault_timers")) and status.get("armed") is False, "native COMMIT resources remain")
        records = status.get("faults", [])
        require(status.get("faults_fired") == (0 if stage == "baseline" else 1) and len(records) == (0 if stage == "baseline" else 1), "native COMMIT positive fault proof missing")
        if stage != "baseline":
            record = records[0]
            require(record.get("mode") == ("stall" if deadline else "drop") and record.get("direction") == ("request" if request_held else "response") and record.get("configured_ms") == (5000 if deadline else 0) and record.get("outcome") == ("canceled" if deadline else "dropped"), "native COMMIT fault phase/outcome mismatch")
            if deadline:
                require(isinstance(record.get("elapsed_ms"), (int, float)) and 3000 <= record["elapsed_ms"] < 5000, "native COMMIT cancellation timing missing")
            require(block.get("retirement") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native COMMIT independent retirement missing")
        if stage in ("unknown", "replay"):
            require(block.get("database") == {"ledger_id": proof["ledger_id"], "charges": 0 if request_held and stage == "unknown" else 1, "pending": 0, "matching": 0 if request_held and stage == "unknown" else 1}, "native COMMIT did not persist exactly one matching charge")
        require(status.get("stopped") is (stage == "cleanup"), "native COMMIT stopped state mismatch")
    require(proof["cleanup"].get("database") == {"namespace": namespace, "passed": True, "remaining": 0}, "native COMMIT namespace remains")
    return {"namespace": namespace, "passed": True, "faults": 1, "charges": 1}


def verify_commit_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-commit-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native COMMIT database cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "remaining": 0, "backend_sessions": 0, "unretired_backends": 0}, "native COMMIT backend/namespace remains or identity differs")
    return proof



def verify_bandwidth_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace and str(uuid.UUID(proof.get("instance_id", ""))) == proof["instance_id"], "native bandwidth identity missing")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native bandwidth backend baseline missing")
    complete = {"status": 200, "body": {"ready": True, "bytes": 65536, "sha256": hashlib.sha256(b"x" * 65536).hexdigest()}}
    require(proof.get("limited", {}).get("response") == complete and proof.get("recovery", {}).get("response") == complete, "native bandwidth complete read/hash missing")
    require(proof.get("timeout", {}).get("response") == {"status": 503, "body": {"error": "dependency_unavailable", "retryable": True}}, "native bandwidth partial read accepted")
    limited_ms = proof["limited"].get("elapsed_ms")
    require(isinstance(limited_ms, (int, float)) and 200 <= limited_ms < 3500, "native bandwidth successful limited deadline missing")
    elapsed = proof["timeout"].get("elapsed_ms")
    require(isinstance(elapsed, (int, float)) and 1000 <= elapsed < 3500, "native bandwidth deadline missing")
    for stage, attempts, active, count in (("baseline", 1, 0, 0), ("limited", 2, 1, 1), ("timeout", 3, 1, 2), ("recovery", 4, 0, 2), ("cleanup", 4, 0, 2)):
        block = proof.get(stage, {})
        status = block if stage == "baseline" else block.get("status", {})
        require(status.get("namespace") == namespace and status.get("instance_id") == proof["instance_id"] and status.get("fixture_mode") == "bandwidth", "native bandwidth instance/mode changed")
        require(status.get("attempts") == attempts and status.get("tls_connections_verified") == attempts and status.get("tls_failures") == 0 and status.get("invalid_frames") == 0, "native bandwidth attempts/TLS proof missing")
        require(all(status.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "bandwidth_pending_transfers", "bandwidth_buffered_bytes", "bandwidth_timers")), "native bandwidth resources remain")
        require(status.get("bandwidth_active_limits") == active and status.get("stopped") is (stage == "cleanup"), "native bandwidth limit/stop state mismatch")
        records = status.get("bandwidth", [])
        require(len(records) == count, "native bandwidth positive rate evidence missing")
        for index, record in enumerate(records):
            rate = 262144 if index == 0 else 16384
            size, duration = record.get("bytes_forwarded"), record.get("elapsed_ms")
            require(record.get("direction") == "response" and record.get("bytes_per_second") == rate and isinstance(size, int) and isinstance(duration, (int, float)) and duration > 0 and 0 < size <= rate * duration / 1000 + 4096, "native bandwidth pacing evidence invalid")
            require(record.get("outcome") == ("active" if stage == "limited" or stage == "timeout" and index == 1 else "cleared"), "native bandwidth limit not cleared")
            require(size >= 65536 and record.get("canceled_transfers") == 0 if index == 0 else 8192 <= size < 65536 and record.get("canceled_transfers", 0) >= 1, "native bandwidth transfer completion/cancellation missing")
        if stage != "baseline":
            require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native bandwidth independent retirement missing")
    return {"namespace": namespace, "passed": True, "limits": 2}


def verify_bandwidth_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-bandwidth-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native bandwidth cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native bandwidth backend remains or identity differs")
    return proof



def verify_read_cut_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace and str(uuid.UUID(proof.get("instance_id", ""))) == proof["instance_id"], "native read-cut identity missing")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native read-cut backend baseline missing")
    complete = {"status": 200, "body": {"ready": True, "bytes": 65536, "sha256": hashlib.sha256(b"x" * 65536).hexdigest()}}
    failed = {"status": 503, "body": {"error": "dependency_unavailable", "retryable": True}}
    for stage, attempts, count, pairs in (("baseline", 1, 0, 0), ("interrupted", 2, 1, 0), ("recovery", 3, 1, 0), ("concurrent", 5, 2, 1), ("final", 6, 2, 1), ("cleanup", 6, 2, 1)):
        block = proof.get(stage, {})
        status = block if stage == "baseline" else block.get("status", {})
        require(status.get("namespace") == namespace and status.get("instance_id") == proof["instance_id"] and status.get("fixture_mode") == "read-cut", "native read-cut instance changed")
        require(status.get("attempts") == attempts and status.get("tls_connections_verified") == attempts and status.get("tls_failures") == 0 and status.get("invalid_frames") == 0, "native read-cut attempts/TLS missing")
        require(all(status.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "bandwidth_active_limits", "bandwidth_pending_transfers", "bandwidth_buffered_bytes", "bandwidth_timers", "active_read_cuts", "read_pair_waiters", "read_pair_timers")), "native read-cut resources remain")
        require(status.get("read_cut_armed") is False and status.get("read_pair_armed") is False and status.get("stopped") is (stage == "cleanup"), "native read-cut control state mismatch")
        require(status.get("read_cuts_fired") == count and status.get("concurrent_read_pairs") == pairs, "native read-cut positive fault/concurrency missing")
        records = status.get("read_cuts", [])
        require(len(records) == count and all(record.get("outcome") == "cut" and record.get("after_bytes") == 2048 and record.get("prefix_flushed_bytes") == 2048 and record.get("frame_bytes") == 65547 for record in records), "native read-cut truncated DataRow evidence missing")
        if stage != "baseline":
            require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native read-cut independent retirement missing")
        if stage not in ("baseline", "cleanup"):
            require(block.get("responses") == ([failed] if stage == "interrupted" else [complete, failed] if stage == "concurrent" else [complete]), "native read-cut partial success or concurrent isolation/recovery missing")
            duration = block.get("elapsed_ms")
            require(isinstance(duration, (int, float)) and 0 <= duration < 3500, "native read-cut completion unbounded")
    return {"namespace": namespace, "passed": True, "cuts": 2, "concurrent_pairs": 1}


def verify_read_cut_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-read-cut-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native read-cut cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native read-cut backend remains or identity differs")
    return proof



def verify_ssl_request_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace and str(uuid.UUID(proof.get("instance_id", ""))) == proof["instance_id"], "native SSLRequest identity missing")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native SSLRequest backend baseline missing")
    failed = {"status": 503, "body": {"error": "dependency_unavailable", "retryable": True}}
    complete = {"status": 200, "body": {"ready": True, "value": 42}}
    for stage, attempts, verified in (("baseline", 1, 1), ("retries", 4, 1), ("queued", 7, 1), ("recovery", 9, 3), ("cleanup", 9, 3)):
        block = proof.get(stage, {})
        status = block if stage == "baseline" else block.get("status", {})
        require(status.get("namespace") == namespace and status.get("instance_id") == proof["instance_id"] and status.get("fixture_mode") == "ssl-request", "native SSLRequest instance changed")
        require(status.get("attempts") == attempts and status.get("tls_connections_verified") == verified and status.get("authentication_challenges_seen") == verified and status.get("invalid_frames") == 0, "native SSLRequest attempts/TLS or pre-authentication boundary missing")
        records = status.get("connection_stalls", [])
        count = status.get("connection_stalls_fired")
        if stage == "baseline":
            require(count == 0 and status.get("connection_attempts") == 1 and status.get("tls_failures") == 0 and status.get("peak_pool_waiting") == 0, "native SSLRequest baseline invalid")
        elif stage == "retries":
            require(count == 3 and status.get("connection_attempts") == 4 and status.get("peak_pool_waiting") == 0, "native SSLRequest retries were not bounded")
        else:
            require(type(count) is int and 4 <= count <= 6 and status.get("connection_attempts") == count + verified and status.get("peak_pool_waiting") == 2, "native SSLRequest queue or connection attempts unbounded")
            require(count == proof["queued"]["status"]["connection_stalls_fired"], "native SSLRequest continued retrying after clear")
        require(status.get("ssl_requests_seen") == status.get("connection_attempts") and status.get("tls_failures") == count, "native SSLRequest phase counts mismatch")
        require(len(records) == count and all(record.get("phase") == "ssl_request" and record.get("leg") == "client" and record.get("configured_ms") == 5000 and record.get("outcome") == "canceled" and isinstance(record.get("elapsed_ms"), (int, float)) and 1000 <= record["elapsed_ms"] < 5000 for record in records), "native SSLRequest positive stall/deadline proof missing")
        require(all(status.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers")), "native SSLRequest resources remain")
        require(status.get("connection_stall_enabled") is (stage in ("retries", "queued")) and status.get("stopped") is (stage == "cleanup"), "native SSLRequest control state mismatch")
        if stage != "baseline":
            require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native SSLRequest independent retirement missing")
        if stage in ("retries", "queued"):
            duration = block.get("elapsed_ms")
            require(block.get("responses") == [failed] * 3 and isinstance(duration, (int, float)) and (5000 <= duration < 10500 if stage == "retries" else 1000 <= duration < 4500), "native SSLRequest deadline/responses missing")
        elif stage == "recovery":
            require(block.get("responses") == [complete] * 2, "native SSLRequest fresh verified recovery missing")
    return {"namespace": namespace, "passed": True, "queued_requests": 3, "faults": proof["queued"]["status"]["connection_stalls_fired"]}


def verify_ssl_request_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-ssl-request-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native SSLRequest cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native SSLRequest backend remains or identity differs")
    return proof



def verify_retry_budget_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace, "native retry budget namespace mismatch")
    for field in ("caller_id", "provider_id"):
        require(str(uuid.UUID(proof.get(field, ""))) == proof[field], "native retry budget identity missing")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native retry budget backend baseline missing")
    require(proof.get("cancellation", {}).get("joined") is True and proof["cancellation"].get("client_aborted") is True, "native retry budget cancellation missing")
    responses = proof.get("outage", {}).get("responses", [])
    require(len(responses) == 6 and sorted(value.get("body", {}).get("attempts", 0) for value in responses) == [1, 1, 1, 1, 2, 2], "native retry budget aggregate amplification mismatch")
    require(all(value == {"status": 503, "body": {"error": "dependency_unavailable", "retryable": True, "attempts": value["body"]["attempts"]}} for value in responses), "native retry budget outage receipt mismatch")
    duration = proof["outage"].get("elapsed_ms")
    require(isinstance(duration, (int, float)) and 1000 <= duration < 5000, "native retry budget request deadlines missing")
    require(proof.get("recovery", {}).get("responses") == [{"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}] * 2, "native retry budget first-attempt recovery missing")
    for stage, originals, attempts, verified in (("baseline", 1, 1, 1), ("cancellation", 2, 2, 1), ("outage", 8, 10, 1), ("recovery", 10, 12, 3), ("cleanup", 10, 12, 3)):
        block = proof.get(stage, {})
        state = block if stage == "baseline" else block.get("status", {})
        caller, provider = state.get("caller", {}), state.get("provider", {})
        require(caller.get("namespace") == namespace and caller.get("instance_id") == proof["caller_id"] and provider.get("namespace") == namespace and provider.get("instance_id") == proof["provider_id"] and provider.get("fixture_mode") == "ssl-request", "native retry budget instance changed")
        require(all(caller.get(key) == value for key, value in (("budget_capacity", 2), ("budget_window_ms", 30000), ("overall_deadline_ms", 4500), ("attempt_deadline_ms", 1500), ("max_attempts", 2))), "native retry budget configuration mismatch")
        failed = stage in ("outage", "recovery", "cleanup")
        require(caller.get("originals") == originals and caller.get("dependency_attempts") == attempts and caller.get("retries_admitted") == (2 if failed else 0) and caller.get("retries_denied") == (4 if failed else 0) and caller.get("remaining") == (0 if failed else 2) and caller.get("window_id") == (1 if failed else 0), "native retry budget shared allowance/cancellation mismatch")
        require(all(caller.get(key) == 0 for key in ("active_requests", "retry_timers", "deadline_timers")) and caller.get("stopped") is (stage == "cleanup"), "native retry budget caller resources remain")
        count = provider.get("connection_stalls_fired")
        require(provider.get("attempts") == attempts and provider.get("tls_connections_verified") == verified and provider.get("authentication_challenges_seen") == verified and provider.get("invalid_frames") == 0, "native retry budget dependency attempts/TLS mismatch")
        if stage in ("baseline", "cancellation"):
            require(count == (0 if stage == "baseline" else 1) and provider.get("peak_pool_waiting") == 0, "native retry budget cancellation retried")
        else:
            require(type(count) is int and 2 <= count <= 9 and 5 <= provider.get("peak_pool_waiting", 0) <= 7 and count == proof["outage"]["status"]["provider"]["connection_stalls_fired"], "native retry budget queue/socket attempts unbounded")
        require(provider.get("connection_attempts") == count + verified and provider.get("ssl_requests_seen") == count + verified and provider.get("tls_failures") == count, "native retry budget pre-TLS fault mismatch")
        records = provider.get("connection_stalls", [])
        require(len(records) == count and all(record.get("phase") == "ssl_request" and record.get("leg") == "client" and record.get("outcome") == "canceled" and record.get("configured_ms") == 5000 and isinstance(record.get("elapsed_ms"), (int, float)) and 1000 <= record["elapsed_ms"] < 5000 for record in records), "native retry budget positive dependency fault missing")
        require(all(provider.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers")) and provider.get("stopped") is (stage == "cleanup") and provider.get("connection_stall_enabled") is (stage in ("cancellation", "outage")), "native retry budget provider resources remain")
        if stage != "baseline":
            require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native retry budget independent retirement missing")
    return {"namespace": namespace, "passed": True, "originals": 6, "extra_attempts": 2}


def verify_retry_budget_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-retry-budget-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native retry budget cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native retry budget backend remains or identity differs")
    return proof



def verify_circuit_breaker_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace, "native circuit breaker namespace mismatch")
    for field in ("caller_id", "provider_id"):
        require(str(uuid.UUID(proof.get(field, ""))) == proof[field], "native circuit breaker identity missing")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native circuit breaker backend baseline missing")
    blocked = {"status": 503, "body": {"error": "circuit_open", "retryable": True, "attempts": 0}}
    failed = {"status": 503, "body": {"error": "dependency_unavailable", "retryable": True, "attempts": 1}}
    require(proof.get("trip", {}).get("responses") == [failed]*2 and proof.get("open", {}).get("responses") == [blocked]*6, "native breaker trip/open receipts missing")
    duration = proof["open"].get("elapsed_ms")
    require(isinstance(duration, (int, float)) and 0 <= duration < 1200, "native breaker fail-fast deadline missing")
    require(proof.get("failed_probe", {}).get("probe") == failed and proof["failed_probe"].get("responses") == [blocked]*4, "native breaker probe isolation missing")
    require(proof.get("cancellation", {}).get("joined") is True and proof["cancellation"].get("client_aborted") is True, "native breaker cancellation missing")
    require(proof.get("recovery", {}).get("responses") == [{"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}]*3, "native breaker successful probe/recovery missing")
    for stage, requests, attempts, count, state, opens, halves, closes, blocked_count, probes in (
            ("baseline", 1, 1, 0, "closed", 0, 0, 0, 0, 0), ("trip", 3, 3, 2, "open", 1, 0, 0, 0, 0), ("open", 9, 3, 2, "open", 1, 0, 0, 6, 0),
            ("failed_probe", 14, 4, 3, "open", 2, 1, 0, 10, 1), ("cancellation", 15, 5, 4, "open", 3, 2, 0, 10, 2), ("recovery", 18, 8, 4, "closed", 3, 3, 1, 10, 3), ("cleanup", 18, 8, 4, "closed", 3, 3, 1, 10, 3)):
        block = proof.get(stage, {})
        status = block if stage == "baseline" else block.get("status", {})
        caller, provider = status.get("caller", {}), status.get("provider", {})
        require(caller.get("namespace") == namespace and caller.get("instance_id") == proof["caller_id"] and provider.get("namespace") == namespace and provider.get("instance_id") == proof["provider_id"] and provider.get("fixture_mode") == "ssl-request", "native breaker identity changed")
        require(all(caller.get(key) == value for key, value in (("failure_threshold", 2), ("cooldown_ms", 3000), ("deadline_ms", 1500), ("requests", requests), ("dependency_attempts", attempts), ("state", state), ("open_transitions", opens), ("half_open_transitions", halves), ("closed_transitions", closes), ("generation", opens+halves+closes), ("blocked", blocked_count), ("probes_started", probes), ("peak_probes", 1 if probes else 0), ("probe_failures", 1 if probes else 0), ("probe_cancellations", 1 if probes >= 2 else 0), ("probe_successes", 1 if closes else 0), ("consecutive_failures", 2 if state == "open" else 0))), "native breaker state/probe transitions mismatch")
        require(all(caller.get(key) == 0 for key in ("active_requests", "active_probes", "deadline_timers")) and caller.get("stopped") is (stage == "cleanup"), "native breaker caller resources remain")
        verified = 4 if stage in ("recovery", "cleanup") else 1
        require(provider.get("attempts") == attempts and provider.get("tls_connections_verified") == verified and provider.get("authentication_challenges_seen") == verified and provider.get("connection_stalls_fired") == count and provider.get("connection_attempts") == count+verified and provider.get("ssl_requests_seen") == count+verified and provider.get("tls_failures") == count and provider.get("invalid_frames") == 0 and provider.get("peak_pool_waiting") == 0, "native breaker independent dependency/fault mismatch")
        records = provider.get("connection_stalls", [])
        require(len(records) == count and all(record.get("phase") == "ssl_request" and record.get("leg") == "client" and record.get("outcome") == "canceled" and record.get("configured_ms") == 5000 and isinstance(record.get("elapsed_ms"), (int, float)) and 1000 <= record["elapsed_ms"] < 5000 for record in records), "native breaker positive dependency fault missing")
        require(all(provider.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers")) and provider.get("stopped") is (stage == "cleanup") and provider.get("connection_stall_enabled") is (stage not in ("baseline", "recovery", "cleanup")), "native breaker provider resources remain")
        if stage != "baseline":
            require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native breaker independent retirement missing")
    for stage, attempts, probes in (("failed_probe", 4, 1), ("cancellation", 5, 2)):
        observed = proof[stage].get("observed", {})
        require(observed.get("namespace") == namespace and observed.get("instance_id") == proof["caller_id"] and observed.get("state") == "half-open" and observed.get("active_probes") == 1 and observed.get("active_requests") == 1 and observed.get("dependency_attempts") == attempts and observed.get("probes_started") == probes, "native breaker half-open probe not observed")
    return {"namespace": namespace, "passed": True, "blocked": 10, "probe_limit": 1}


def verify_circuit_breaker_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-circuit-breaker-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native circuit breaker cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native circuit breaker backend remains or identity differs")
    return proof



def verify_bulkhead_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace, "native bulkhead namespace mismatch")
    for field in ("caller_id", "provider_id"):
        require(str(uuid.UUID(proof.get(field, ""))) == proof[field], "native bulkhead identity missing")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native bulkhead backend baseline missing")
    require(proof.get("cancellation", {}).get("joined") is True and proof["cancellation"].get("client_aborted") is True, "native bulkhead queued cancellation missing")
    responses = proof.get("outage", {}).get("responses", [])
    require(len(responses) == 6 and sorted(value.get("body", {}).get("error", "") for value in responses) == ["bulkhead_rejected"]*2 + ["dependency_unavailable"]*2 + ["queue_timeout"]*2, "native bulkhead rejection/queue/dependency outcomes missing")
    for value in responses:
        error = value["body"]["error"]; duration = value.get("elapsed_ms")
        require(value.get("status") == 503 and value["body"] == {"error": error, "retryable": True, "attempts": 1 if error == "dependency_unavailable" else 0} and isinstance(duration, (int, float)) and 0 <= duration < 3000, "native bulkhead attempt/deadline mismatch")
        require(duration < 750 if error == "bulkhead_rejected" else 800 <= duration < 1500 if error == "queue_timeout" else 1000 <= duration, "native bulkhead queue/rejection not bounded")
    health = proof["outage"].get("health", [])
    require(len(health) == 3 and all(value.get("status") == 200 and value.get("body") == {"ready": True, "active_dependencies": 2} and isinstance(value.get("elapsed_ms"), (int, float)) and 0 <= value["elapsed_ms"] < 750 for value in health), "native bulkhead healthy-route isolation missing")
    require(proof.get("recovery", {}).get("responses") == [{"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}]*2, "native bulkhead recovered capacity missing")
    for stage, requests, attempts, peak_queue, admissions, timeouts, rejects in (("baseline", 1, 1, 0, 0, 0, 0), ("cancellation", 4, 3, 1, 1, 0, 0), ("outage", 10, 5, 2, 3, 2, 2), ("recovery", 12, 7, 2, 3, 2, 2), ("cleanup", 12, 7, 2, 3, 2, 2)):
        block = proof.get(stage, {})
        status = block if stage == "baseline" else block.get("status", {})
        caller, provider = status.get("caller", {}), status.get("provider", {})
        require(caller.get("namespace") == namespace and caller.get("instance_id") == proof["caller_id"] and provider.get("namespace") == namespace and provider.get("instance_id") == proof["provider_id"] and provider.get("fixture_mode") == "ssl-request", "native bulkhead identity changed")
        require(all(caller.get(key) == value for key, value in (("capacity", 2), ("queue_capacity", 2), ("queue_deadline_ms", 1000), ("attempt_deadline_ms", 2500), ("overall_deadline_ms", 3500), ("requests", requests), ("dependency_attempts", attempts), ("permits_granted", attempts), ("permits_released", attempts), ("peak_active", 1 if stage == "baseline" else 2), ("peak_queue", peak_queue), ("queued_admissions", admissions), ("queue_timeouts", timeouts), ("rejected", rejects), ("queue_cancellations", 0 if stage == "baseline" else 1), ("health_requests", 0 if stage in ("baseline", "cancellation") else 3))), "native bulkhead permit/queue bounds mismatch")
        require(all(caller.get(key) == 0 for key in ("active_requests", "active_dependencies", "queued_requests", "queue_timers", "deadline_timers")) and caller.get("stopped") is (stage == "cleanup"), "native bulkhead caller resources remain")
        count = provider.get("connection_stalls_fired"); verified = 3 if stage in ("recovery", "cleanup") else 1
        require(provider.get("attempts") == attempts and provider.get("tls_connections_verified") == verified and provider.get("authentication_challenges_seen") == verified and provider.get("invalid_frames") == 0, "native bulkhead dependency attempt/TLS mismatch")
        require(count == 0 and provider.get("peak_pool_waiting") == 0 if stage == "baseline" else type(count) is int and (1 <= count <= 2 if stage == "cancellation" else 2 <= count <= 4) and provider.get("peak_pool_waiting") == 1, "native bulkhead positive dependency pressure missing")
        if stage in ("recovery", "cleanup"):
            require(count == proof["outage"]["status"]["provider"]["connection_stalls_fired"], "native bulkhead recovery kept faulting")
        require(provider.get("connection_attempts") == count+verified and provider.get("ssl_requests_seen") == count+verified and provider.get("tls_failures") == count, "native bulkhead pre-TLS fault mismatch")
        records = provider.get("connection_stalls", [])
        require(len(records) == count and all(record.get("phase") == "ssl_request" and record.get("leg") == "client" and record.get("outcome") == "canceled" and record.get("configured_ms") == 5000 and isinstance(record.get("elapsed_ms"), (int, float)) and 1000 <= record["elapsed_ms"] < 5000 for record in records), "native bulkhead positive dependency fault missing")
        require(all(provider.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers")) and provider.get("stopped") is (stage == "cleanup") and provider.get("connection_stall_enabled") is (stage in ("cancellation", "outage")), "native bulkhead provider resources remain")
        if stage != "baseline":
            require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native bulkhead independent retirement missing")
    for stage, queued in (("cancellation", 1), ("outage", 2)):
        observed = proof[stage].get("observed", {})
        require(observed.get("namespace") == namespace and observed.get("instance_id") == proof["caller_id"] and observed.get("active_dependencies") == 2 and observed.get("queued_requests") == queued, "native bulkhead active/queued capacity not observed")
    return {"namespace": namespace, "passed": True, "capacity": 2, "queue_capacity": 2}


def verify_bulkhead_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-bulkhead-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native bulkhead cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native bulkhead backend remains or identity differs")
    return proof



def verify_combined_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace, "native combined namespace mismatch")
    for field in ("caller_id", "provider_id"):
        require(str(uuid.UUID(proof.get(field, ""))) == proof[field], "native combined identity missing")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native combined backend baseline missing")
    blocked = {"status": 503, "body": {"error": "circuit_open", "retryable": True, "attempts": 0}}
    failed = {"status": 503, "body": {"error": "dependency_unavailable", "retryable": True, "attempts": 1}}
    for stage in ("initial_cancellation", "cancellation"):
        require(proof.get(stage, {}).get("joined") is True and proof[stage].get("client_aborted") is True, "native combined cancellation missing")
    responses = proof.get("outage", {}).get("responses", [])
    require(len(responses) == 6 and sorted(value.get("body", {}).get("error", "") for value in responses) == ["bulkhead_rejected"]*2+["dependency_unavailable"]*2+["queue_timeout"]*2, "native combined overload outcomes mismatch")
    for value in responses:
        error, duration = value["body"]["error"], value.get("elapsed_ms")
        require(value.get("status") == 503 and value["body"] == {"error": error, "retryable": True, "attempts": 2 if error == "dependency_unavailable" else 0} and isinstance(duration, (int, float)) and 0 <= duration < 4500, "native combined protected retry/deadline mismatch")
        require(duration < 750 if error == "bulkhead_rejected" else 800 <= duration < 1500 if error == "queue_timeout" else duration >= 2500, "native combined queue/retry deadline missing")
    health = proof["outage"].get("health", [])
    require(len(health) == 3 and all(value.get("status") == 200 and value.get("body") == {"ready": True, "active_dependencies": 2} and isinstance(value.get("elapsed_ms"), (int, float)) and 0 <= value["elapsed_ms"] < 750 for value in health), "native combined healthy-route isolation missing")
    duration = proof.get("open", {}).get("elapsed_ms")
    require(proof["open"].get("responses") == [blocked]*6 and isinstance(duration, (int, float)) and 0 <= duration < 1200, "native combined open-state fail-fast missing")
    require(proof.get("failed_probe", {}).get("probe") == failed and proof["failed_probe"].get("responses") == [blocked]*4, "native combined probe retried or exceeded limit")
    require(proof.get("recovery", {}).get("responses") == [{"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}]*3, "native combined successful recovery missing")
    for stage, requests, attempts, state, opens, halves, closes, probes, denied, canceled in (
            ("baseline", 1, 1, "closed", 0, 0, 0, 0, 0, 0), ("initial_cancellation", 4, 3, "closed", 0, 0, 0, 0, 0, 3), ("outage", 10, 7, "open", 1, 0, 0, 0, 0, 3), ("open", 16, 7, "open", 1, 0, 0, 0, 6, 3), ("failed_probe", 21, 8, "open", 2, 1, 0, 1, 10, 3), ("cancellation", 22, 9, "open", 3, 2, 0, 2, 10, 4), ("recovery", 25, 12, "closed", 3, 3, 1, 3, 10, 4), ("cleanup", 25, 12, "closed", 3, 3, 1, 3, 10, 4)):
        block = proof.get(stage, {})
        status = block if stage == "baseline" else block.get("status", {})
        caller, provider = status.get("caller", {}), status.get("provider", {})
        require(caller.get("namespace") == namespace and caller.get("instance_id") == proof["caller_id"] and provider.get("namespace") == namespace and provider.get("instance_id") == proof["provider_id"] and provider.get("fixture_mode") == "ssl-request", "native combined identity changed")
        stressed = stage not in ("baseline", "initial_cancellation")
        expected = {"capacity": 2, "queue_capacity": 2, "queue_deadline_ms": 1000, "attempt_deadline_ms": 1500, "overall_deadline_ms": 4500, "failure_threshold": 4, "cooldown_ms": 3000, "budget_capacity": 2, "budget_window_ms": 30000,
                    "requests": requests, "dependency_attempts": attempts, "permit_protected_attempts": attempts, "permits_granted": attempts, "permits_released": attempts,
                    "peak_active": 1 if stage == "baseline" else 2, "peak_queue": 2 if stressed else 1 if stage == "initial_cancellation" else 0,
                    "queued_admissions": 3 if stressed else 1 if stage == "initial_cancellation" else 0, "queue_timeouts": 2 if stressed else 0, "rejected": 2 if stressed else 0, "queue_cancellations": 0 if stage == "baseline" else 1, "health_requests": 3 if stressed else 0,
                    "state": state, "generation": opens+halves+closes, "consecutive_failures": 4 if state == "open" else 0, "open_transitions": opens, "half_open_transitions": halves, "closed_transitions": closes, "blocked": denied, "blocked_after_acquire": 0,
                    "probes_started": probes, "peak_probes": 1 if probes else 0, "probe_failures": 1 if probes else 0, "probe_cancellations": 1 if probes >= 2 else 0, "probe_successes": 1 if closes else 0, "request_cancellations": canceled,
                    "retries_admitted": 2 if stressed else 0, "retries_denied": 0, "remaining": 0 if stressed else 2, "window_id": 1 if stressed else 0}
        require(all(caller.get(key) == value for key, value in expected.items()), "native combined policy/permit/retry/probe accounting mismatch")
        require(all(caller.get(key) == 0 for key in ("active_requests", "active_dependencies", "active_probes", "queued_requests", "queue_timers", "retry_timers", "deadline_timers")) and caller.get("stopped") is (stage == "cleanup"), "native combined caller resources remain")
        count = provider.get("connection_stalls_fired"); verified = 4 if stage in ("recovery", "cleanup") else 1
        require(provider.get("attempts") == attempts and provider.get("tls_connections_verified") == verified and provider.get("authentication_challenges_seen") == verified and provider.get("invalid_frames") == 0, "native combined independent dependency attempts/TLS mismatch")
        require(count == 0 and provider.get("peak_pool_waiting") == 0 if stage == "baseline" else type(count) is int and (1 <= count <= 2 if stage == "initial_cancellation" else 2 <= count <= attempts-verified) and 1 <= provider.get("peak_pool_waiting", 0) <= 3, "native combined positive fault/queue bounds missing")
        if stage in ("open", "recovery", "cleanup"):
            original = "outage" if stage == "open" else "cancellation"
            require(count == proof[original]["status"]["provider"]["connection_stalls_fired"], "native combined unrequested dependency attempts")
        require(provider.get("connection_attempts") == count+verified and provider.get("ssl_requests_seen") == count+verified and provider.get("tls_failures") == count, "native combined pre-TLS fault mismatch")
        records = provider.get("connection_stalls", [])
        require(len(records) == count and all(record.get("phase") == "ssl_request" and record.get("leg") == "client" and record.get("outcome") == "canceled" and record.get("configured_ms") == 5000 and isinstance(record.get("elapsed_ms"), (int, float)) and 1000 <= record["elapsed_ms"] < 5000 for record in records), "native combined positive dependency fault missing")
        require(all(provider.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers")) and provider.get("stopped") is (stage == "cleanup") and provider.get("connection_stall_enabled") is (stage not in ("baseline", "recovery", "cleanup")), "native combined provider resources remain")
        if stage != "baseline":
            require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native combined independent retirement missing")
    for stage, queued in (("initial_cancellation", 1), ("outage", 2)):
        observed = proof[stage].get("observed", {})
        require(observed.get("namespace") == namespace and observed.get("instance_id") == proof["caller_id"] and observed.get("active_dependencies") == 2 and observed.get("queued_requests") == queued, "native combined bulkhead capacity not observed")
    for stage, attempts in (("failed_probe", 8), ("cancellation", 9)):
        observed = proof[stage].get("observed", {})
        require(observed.get("namespace") == namespace and observed.get("instance_id") == proof["caller_id"] and observed.get("state") == "half-open" and observed.get("active_probes") == 1 and observed.get("active_dependencies") == 1 and observed.get("dependency_attempts") == attempts, "native combined probe not protected by a permit")
    return {"namespace": namespace, "passed": True, "retries": 2, "probe_limit": 1, "capacity": 2}


def verify_combined_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-combined-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native combined cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native combined backend remains or identity differs")
    return proof



def verify_isolation_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace, "native isolation namespace mismatch")
    identity_maps = [proof.get(field, {}) for field in ("provider_ids", "dependency_ids")]
    ids = [proof.get("caller_id", "")]
    for values in identity_maps:
        require(set(values) == {"a", "b"}, "native isolation identities missing")
        ids.extend(values.values())
    require(len(set(ids)) == 5 and all(str(uuid.UUID(value)) == value for value in ids), "native isolation identities reused or invalid")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native isolation backend baseline missing")
    phases = proof.get("phases", [])
    require(len(phases) == 2 and [(block.get("faulted"), block.get("healthy")) for block in phases] == [("a", "b"), ("b", "a")], "native isolation must exercise both dependency directions")
    complete = {"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}
    blocked = {"status": 503, "body": {"error": "circuit_open", "retryable": True, "attempts": 0}}
    faults = {"a": 0, "b": 0}
    def caller_identity(caller):
        require(caller.get("namespace") == namespace and caller.get("instance_id") == proof["caller_id"] and set(caller.get("dependencies", {})) == {"a", "b"}, "native isolation application changed")
        for name in ("a", "b"):
            value = caller["dependencies"][name]
            require(value.get("namespace") == namespace and value.get("instance_id") == proof["dependency_ids"][name], "native isolation dependency policy changed")
    def snapshot(block, expected, health, stopped=False, baseline=False):
        status = block if baseline else block.get("status", {})
        caller, providers = status.get("caller", {}), status.get("providers", {})
        caller_identity(caller)
        require(caller.get("stopped") is stopped and caller.get("health_requests") == health and set(providers) == {"a", "b"}, "native isolation application/health evidence mismatch")
        for name, (requests, attempts, state) in expected.items():
            value, provider = caller["dependencies"][name], providers[name]
            faulted, recovered = state != "untouched", state == "recovered"
            config = {"capacity": 2, "queue_capacity": 2, "queue_deadline_ms": 1000, "attempt_deadline_ms": 1500, "overall_deadline_ms": 4500, "failure_threshold": 4, "cooldown_ms": 3000, "budget_capacity": 2, "budget_window_ms": 30000}
            counters = {"requests": requests, "dependency_attempts": attempts, "permit_protected_attempts": attempts, "permits_granted": attempts, "permits_released": attempts,
                        "peak_active": 2 if faulted else 1, "peak_queue": 2 if faulted else 0, "queued_admissions": 2 if faulted else 0, "queue_timeouts": 2 if faulted else 0, "rejected": 2 if faulted else 0,
                        "queue_cancellations": 0, "request_cancellations": 0, "health_requests": 0, "blocked_after_acquire": 0, "blocked": 6 if state in ("open", "recovered") else 0,
                        "retries_admitted": 2 if faulted else 0, "retries_denied": 0, "remaining": 0 if faulted else 2, "window_id": 1 if faulted else 0,
                        "state": "closed" if recovered or not faulted else "open", "generation": 3 if recovered else 1 if faulted else 0, "consecutive_failures": 0 if recovered or not faulted else 4,
                        "open_transitions": 1 if faulted else 0, "half_open_transitions": 1 if recovered else 0, "closed_transitions": 1 if recovered else 0, "probes_started": 1 if recovered else 0,
                        "probe_successes": 1 if recovered else 0, "peak_probes": 1 if recovered else 0, "probe_failures": 0, "probe_cancellations": 0}
            require(all(value.get(key) == target for key, target in {**config, **counters}.items()), "native isolation per-dependency policy/permit counters mismatch")
            require(value.get("stopped") is stopped and all(value.get(key) == 0 for key in ("active_requests", "active_dependencies", "active_probes", "queued_requests", "queue_timers", "retry_timers", "deadline_timers")), "native isolation caller resources not retired")
            require(provider.get("namespace") == namespace and provider.get("instance_id") == proof["provider_ids"][name] and provider.get("fixture_mode") == "ssl-request", "native isolation provider changed")
            count = provider.get("connection_stalls_fired")
            if faulted and not faults[name]:
                require(type(count) is int and 2 <= count <= 4, "native isolation positive bounded fault evidence missing")
                faults[name] = count
            require(count == faults[name] and provider.get("connection_stall_enabled") is (faulted and not recovered), "native isolation fault escaped its dependency")
            verified = attempts - (4 if faulted else 0)
            require(provider.get("attempts") == attempts and provider.get("tls_connections_verified") == verified and provider.get("authentication_challenges_seen") == verified and provider.get("invalid_frames") == 0,
                    "native isolation provider attempts/TLS mismatch")
            require(provider.get("connection_attempts") == count + verified and provider.get("ssl_requests_seen") == count + verified and provider.get("tls_failures") == count, "native isolation connection counts mismatch")
            records = provider.get("connection_stalls", [])
            require(len(records) == count and all(record.get("phase") == "ssl_request" and record.get("leg") == "client" and record.get("outcome") == "canceled" and record.get("configured_ms") == 5000 and isinstance(record.get("elapsed_ms"), (int, float)) and 1000 <= record["elapsed_ms"] < 5000 for record in records), "native isolation SSLRequest faults missing")
            require(provider.get("stopped") is stopped and all(provider.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers")), "native isolation provider resources not retired")
            require(provider.get("peak_pool_waiting") == 0 if not faulted else 1 <= provider.get("peak_pool_waiting", 0) <= 3, "native isolation provider queue bounds missing")
        if not baseline:
            require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native isolation independent backend retirement missing")
    snapshot(proof.get("baseline", {}), {"a": (1, 1, "untouched"), "b": (1, 1, "untouched")}, 0, baseline=True)
    expected = [
        ({"a": (7, 5, "outage"), "b": (7, 7, "untouched")}, {"a": (13, 5, "open"), "b": (10, 10, "untouched")}, {"a": (16, 8, "recovered"), "b": (10, 10, "untouched")}),
        ({"a": (22, 14, "recovered"), "b": (16, 14, "outage")}, {"a": (25, 17, "recovered"), "b": (22, 14, "open")}, {"a": (25, 17, "recovered"), "b": (25, 17, "recovered")})]
    for index, block in enumerate(phases):
        faulted, healthy = block["faulted"], block["healthy"]
        for field in ("observed", "after_healthy"):
            observed = block.get(field, {}); caller_identity(observed)
            require(observed["dependencies"][faulted].get("active_dependencies") == 2, "native isolation healthy traffic did not overlap outage")
            require(observed["dependencies"][healthy].get("active_dependencies") == 0 and observed["dependencies"][healthy].get("queued_requests") == 0, "native isolation healthy dependency saturated")
        require(block["observed"]["dependencies"][faulted].get("queued_requests") == 2, "native isolation faulted queue not exercised")
        after = block["after_healthy"]["dependencies"][healthy]
        require(after.get("requests") == expected[index][0][healthy][0] and after.get("dependency_attempts") == expected[index][0][healthy][1], "native isolation healthy calls did not complete during outage")
        for stage, counters in zip(("outage", "open", "recovery"), expected[index]):
            snapshot(block.get(stage, {}), counters, (index+1)*3)
        outage, opened, recovery = block["outage"], block["open"], block["recovery"]
        responses = outage.get("responses", [])
        require(len(responses) == 6 and sorted(value.get("body", {}).get("error", "") for value in responses) == ["bulkhead_rejected"]*2 + ["dependency_unavailable"]*2 + ["queue_timeout"]*2, "native isolation overload outcomes mismatch")
        for value in responses:
            error, duration = value["body"]["error"], value.get("elapsed_ms")
            require(value.get("status") == 503 and value["body"] == {"error": error, "retryable": True, "attempts": 2 if error == "dependency_unavailable" else 0} and isinstance(duration, (int, float)) and 0 <= duration < 4500, "native isolation faulted request deadlines missing")
        for stage, count in ((outage, 6), (opened, 3)):
            values = stage.get("healthy_responses", [])
            require(len(values) == count and all(value.get("status") == complete["status"] and value.get("body") == complete["body"] and isinstance(value.get("elapsed_ms"), (int, float)) and 0 <= value["elapsed_ms"] < 750 for value in values), "native isolation healthy route failed or latency exceeded")
        health = outage.get("health", [])
        require(len(health) == 3 and all(value.get("status") == 200 and value.get("body") == {"ready": True, "active_dependencies": {faulted: 2, healthy: 0}} and isinstance(value.get("elapsed_ms"), (int, float)) and 0 <= value["elapsed_ms"] < 750 for value in health), "native isolation health isolation missing")
        require(opened.get("responses") == [blocked]*6 and isinstance(opened.get("elapsed_ms"), (int, float)) and 0 <= opened["elapsed_ms"] < 1200, "native isolation open circuit did not fail fast")
        require(recovery.get("responses") == [complete]*3, "native isolation half-open recovery missing")
    snapshot(proof.get("cleanup", {}), expected[-1][-1], 6, stopped=True)
    return {"namespace": namespace, "passed": True, "dependencies": 2, "directions": 2}


def verify_isolation_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-isolation-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native isolation cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native isolation backend remains or identity differs")
    return proof


def verify_repeated_proof(proof, report):
    namespace = report["run_id"]
    require(proof.get("namespace") == namespace, "native repeated namespace mismatch")
    for field in ("caller_id", "provider_id"):
        require(str(uuid.UUID(proof.get(field, ""))) == proof[field], "native repeated identity missing")
    pids = proof.get("backend_baseline_pids")
    require(isinstance(pids, list) and len(pids) <= 128 and all(type(pid) is int and pid > 0 for pid in pids), "native repeated backend baseline missing")
    complete = {"status": 200, "body": {"ready": True, "value": 42, "attempts": 1}}
    blocked = {"status": 503, "body": {"error": "circuit_open", "retryable": True, "attempts": 0}}
    cycles = proof.get("cycles", [])
    require(len(cycles) == 3 and [block.get("index") for block in cycles] == [1,2,3], "three native repeated cycles required")
    def snapshot(block, requests, attempts, grants, cycle, recovered, denied, verified, stopped=False, baseline=False, early=False):
        status = block if baseline else block.get("status", {})
        caller, provider = status.get("caller", {}), status.get("provider", {})
        require(caller.get("namespace") == namespace and caller.get("instance_id") == proof["caller_id"] and provider.get("namespace") == namespace and provider.get("instance_id") == proof["provider_id"] and provider.get("fixture_mode") == "ssl-request", "native repeated instance changed")
        closed = recovered or baseline or early
        probes = cycle if recovered or early else max(0,cycle-1)
        expected = {"capacity": 2, "queue_capacity": 2, "queue_deadline_ms": 1000, "attempt_deadline_ms": 1500, "overall_deadline_ms": 4500, "failure_threshold": 4, "cooldown_ms": 3000, "budget_capacity": 2, "budget_window_ms": 30000,
                    "requests": requests, "dependency_attempts": attempts, "permit_protected_attempts": attempts, "permits_granted": grants, "permits_released": grants,
                    "peak_active": 1 if baseline else 2, "peak_queue": 0 if baseline else 2, "queued_admissions": cycle*2, "queue_timeouts": cycle*2, "rejected": cycle*2, "queue_cancellations": 0, "health_requests": cycle*3,
                    "state": "closed" if closed else "open", "generation": cycle+2*probes, "consecutive_failures": 1 if early and not recovered else 0 if closed else 4,
                    "open_transitions": cycle, "half_open_transitions": probes, "closed_transitions": probes, "probes_started": probes, "probe_successes": probes, "peak_probes": 1 if probes else 0, "probe_failures": 0, "probe_cancellations": 0,
                    "blocked": denied, "blocked_after_acquire": 0, "request_cancellations": 0, "retries_admitted": cycle*2, "retries_denied": 0 if baseline or cycle==1 and not early else 1,
                    "remaining": 2 if baseline else 0, "window_id": cycle}
        require(all(caller.get(key) == value for key, value in expected.items()), "native repeated policy/permit/window counters mismatch")
        require(all(caller.get(key) == 0 for key in ("active_requests", "active_dependencies", "active_probes", "queued_requests", "queue_timers", "retry_timers", "deadline_timers")) and caller.get("stopped") is stopped, "native repeated caller resources accumulated")
        count = provider.get("connection_stalls_fired")
        require(provider.get("attempts") == attempts and provider.get("tls_connections_verified") == verified and provider.get("authentication_challenges_seen") == verified and provider.get("invalid_frames") == 0, "native repeated dependency/TLS counters mismatch")
        require(count == 0 and provider.get("peak_pool_waiting") == 0 if baseline else type(count) is int and cycle <= count <= cycle*4+1 and 1 <= provider.get("peak_pool_waiting",0) <= 3, "native repeated positive fault/queue bounds missing")
        require(provider.get("connection_attempts") == count+verified and provider.get("ssl_requests_seen") == count+verified and provider.get("tls_failures") == count, "native repeated connection counts mismatch")
        records = provider.get("connection_stalls", [])
        require(len(records) == count and all(record.get("phase") == "ssl_request" and record.get("leg") == "client" and record.get("outcome") == "canceled" and record.get("configured_ms") == 5000 and isinstance(record.get("elapsed_ms"), (int, float)) and 1000 <= record["elapsed_ms"] < 5000 for record in records), "native repeated positive dependency faults missing")
        require(all(provider.get(key) == 0 for key in ("active_queries", "pool_total", "pool_waiting", "connections", "pending_connections", "pending_authentication", "active_connection_stalls", "connection_stall_timers")) and provider.get("stopped") is stopped and provider.get("connection_stall_enabled") is (not closed or early and not recovered), "native repeated provider resources accumulated")
        if not baseline:
            require(block.get("database") == {"namespace": namespace, "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native repeated independent retirement missing")
        return count
    snapshot(proof.get("baseline", {}),1,1,1,0,True,0,1,baseline=True)
    early = proof.get("before_expiry", {})
    require(early.get("response") == {"status": 503, "body": {"error": "retry_budget_exhausted", "retryable": True, "attempts": 1}}, "native repeated early retry refill")
    snapshot(early,17,9,10,1,False,6,4,early=True)
    early_caller = early["status"]["caller"]
    require(isinstance(early_caller.get("retry_window_age_ms"), (int,float)) and 0 <= early_caller["retry_window_age_ms"] < 30000 and early_caller.get("retry_window_remaining_ms",0) > 0, "native repeated before-expiry timing missing")
    require(early.get("recovery",{}).get("response") == complete, "native repeated early refusal recovery missing")
    snapshot(early["recovery"],18,10,11,1,True,6,5,early=True)
    maxima = []
    previous_count = 0
    for block in cycles:
        cycle = block["index"]; extra = 2 if cycle>1 else 0
        requests, attempts = 15*cycle-8+extra, 7*cycle-2+extra
        grants = attempts + (1 if cycle>1 else 0); verified = 1+3*(cycle-1)+(1 if cycle>1 else 0)
        outage, opened, recovery = block.get("outage",{}), block.get("open",{}), block.get("recovery",{})
        count = snapshot(outage,requests,attempts,grants,cycle,False,6*(cycle-1),verified)
        require(previous_count+1 <= count <= previous_count+4, "native repeated outage did not introduce bounded new faults")
        responses = outage.get("responses",[])
        require(len(responses)==6 and sorted(value.get("body",{}).get("error","") for value in responses)==["bulkhead_rejected"]*2+["dependency_unavailable"]*2+["queue_timeout"]*2, "native repeated overload outcomes mismatch")
        for value in responses:
            error, duration = value["body"]["error"], value.get("elapsed_ms")
            require(value.get("status")==503 and value["body"]=={"error":error,"retryable":True,"attempts":2 if error=="dependency_unavailable" else 0} and isinstance(duration,(int,float)) and 0 <= duration < 4500, "native repeated attempt deadlines missing")
        health = outage.get("health",[])
        require(len(health)==3 and all(value.get("status")==200 and value.get("body")=={"ready":True,"active_dependencies":2} and isinstance(value.get("elapsed_ms"),(int,float)) and 0 <= value["elapsed_ms"]<750 for value in health), "native repeated health isolation missing")
        maxima.append(max(value["elapsed_ms"] for value in health))
        observed=outage.get("observed",{})
        require(observed.get("namespace")==namespace and observed.get("instance_id")==proof["caller_id"] and observed.get("active_dependencies")==2 and observed.get("queued_requests")==2, "native repeated capacity not observed")
        require(opened.get("responses")==[blocked]*6 and isinstance(opened.get("elapsed_ms"),(int,float)) and 0 <= opened["elapsed_ms"]<1200, "native repeated circuit fail-fast missing")
        require(snapshot(opened,requests+6,attempts,grants,cycle,False,cycle*6,verified)==count, "native repeated open circuit called dependency")
        require(recovery.get("responses")==[complete]*3, "native repeated successful probe/recovery missing")
        require(snapshot(recovery,requests+9,attempts+3,grants+3,cycle,True,cycle*6,verified+3)==count, "native repeated recovery kept faulting")
        previous_count=count
        if cycle==1:
            previous_count=early["recovery"]["status"]["provider"]["connection_stalls_fired"]
            require(previous_count==count+1, "native repeated before-expiry fault missing")
        else:
            expiry=block.get("expiry",{}); before,after=expiry.get("before",{}),expiry.get("after",{})
            for value in (before,after):
                require(value.get("namespace")==namespace and value.get("instance_id")==proof["caller_id"] and value.get("window_id")==cycle-1 and value.get("remaining")==0, "native repeated expiry reset window early")
            age=before.get("retry_window_age_ms"); elapsed=expiry.get("elapsed_ms")
            require(isinstance(age,(int,float)) and isinstance(elapsed,(int,float)) and max(0,30000-age)-200 <= elapsed < 35000 and isinstance(after.get("retry_window_age_ms"),(int,float)) and after["retry_window_age_ms"]>=30000 and after.get("retry_window_remaining_ms")==0, "native repeated real window expiry missing")
    require(max(maxima)-min(maxima)<=250, "native repeated healthy latency regressed across cycles")
    require(snapshot(proof.get("cleanup",{}),48,24,25,3,True,18,11,stopped=True)==previous_count, "native repeated cleanup changed fault counts")
    return {"namespace":namespace,"passed":True,"cycles":3,"windows":3}


def verify_repeated_database(repo, report, proof_path):
    result = subprocess.run(["node", str(repo / "examples/scenario-http-write/test/native-repeated-acceptance.mjs"), "check", report["run_id"], str(proof_path)], capture_output=True, text=True, timeout=15)
    require(result.returncode == 0, "independent native repeated cleanup failed")
    proof = json.loads(result.stdout)
    require(proof == {"namespace": report["run_id"], "passed": True, "backend_sessions": 0, "unretired_backends": 0}, "native repeated backend remains or identity differs")
    return proof


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--cli", type=Path)
    parser.add_argument("--check-runner", action="store_true")
    args = parser.parse_args()
    def interrupted(_signal, _frame):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupted)
    args.out.mkdir(parents=True, exist_ok=True, mode=0o700)
    summary = {"version": 20, "status": "failed", "runs": {}}
    try:
        try:
            api, provenance = runner_configuration()
        except (OSError, ValueError):
            summary["status"] = "blocked"
            raise
        summary.update(provenance)
        if args.check_runner:
            summary["status"] = "ready"
            return
        require(args.cli and args.cli.is_file(), "candidate CLI binary is required")
        summary["cli_sha256"] = digest(args.cli)
        summary["git_head"] = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=args.repo, text=True).strip()
        summary["working_tree_dirty"] = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=args.repo))
        variants = manifests(args.repo.resolve(), args.out.resolve(), os.environ.get("FAAS_SCENARIO_ACCEPTANCE_PROJECT", "native-checkout"))
        for case, (manifest, project) in variants.items():
            suite, _, mode, _ = case_contract(case)
            run_dir = args.out / case
            run_dir.mkdir(mode=0o700)
            report = run_dir / "results.json"
            common = [str(args.cli), "test", "--manifest", str(manifest), "--suite", suite]
            with auth_environment(case, run_dir) as environment:
                subprocess.run(common + ["--validate"], check=True, timeout=60, env=environment)
                subprocess.run(common + ["--preflight"], check=True, timeout=60, env=environment)
                marker = uuid.uuid4().hex if mode == "cancel" else None
                code, issued = execute(common + ["--max-workload-minutes", "20", "--report", str(report),
                                               "--html", str(run_dir / "results.html"), "--junit", str(run_dir / "results.xml")], run_dir / "run.log", marker, environment=environment, **({"checkpoint": lambda: confirm_pool_checkpoint(run_dir / "pool-cancel-proof.json", marker)} if case == "tcp-flight" else {}))
            document = json.loads(report.read_text())
            receipt = cleanup_identity(document, project)
            result = {"run_id": receipt["run_id"], "exit_code": code, "cancellation_issued": issued,
                      "manifest_sha256": digest(manifest), "report_sha256": digest(report), "verified": False}
            summary["runs"][case] = result
            try:
                verify_report(document, case, code, issued, project)
                verify_artifacts(run_dir, document, case)
                if case == "tcp-flight":
                    proof_path = run_dir / "pool-cancel-proof.json"
                    result["active_cancellation"] = verify_pool_cancel_proof(json.loads(proof_path.read_text()), receipt, marker)
                    result["pool_cancel_proof_sha256"] = digest(proof_path)
                if case == "isolation":
                    isolation_path = run_dir / "isolation-proof.json"
                    result["isolation"] = verify_isolation_proof(json.loads(isolation_path.read_text()), receipt)
                    result["isolation_proof_sha256"] = digest(isolation_path)
                if case == "repeated":
                    repeated_path = run_dir / "repeated-proof.json"
                    result["repeated"] = verify_repeated_proof(json.loads(repeated_path.read_text()), receipt)
                    result["repeated_proof_sha256"] = digest(repeated_path)
                if case == "combined":
                    combined_path = run_dir / "combined-proof.json"
                    result["combined"] = verify_combined_proof(json.loads(combined_path.read_text()), receipt)
                    result["combined_proof_sha256"] = digest(combined_path)
                if case == "bulkhead":
                    bulkhead_path = run_dir / "bulkhead-proof.json"
                    result["bulkhead"] = verify_bulkhead_proof(json.loads(bulkhead_path.read_text()), receipt)
                    result["bulkhead_proof_sha256"] = digest(bulkhead_path)
                if case == "circuit-breaker":
                    breaker_path = run_dir / "circuit-breaker-proof.json"
                    result["circuit_breaker"] = verify_circuit_breaker_proof(json.loads(breaker_path.read_text()), receipt)
                    result["circuit_breaker_proof_sha256"] = digest(breaker_path)
                if case == "retry-budget":
                    budget_path = run_dir / "retry-budget-proof.json"
                    result["retry_budget"] = verify_retry_budget_proof(json.loads(budget_path.read_text()), receipt)
                    result["retry_budget_proof_sha256"] = digest(budget_path)
                if case == "ssl-request":
                    ssl_path = run_dir / "ssl-request-proof.json"
                    result["ssl_request"] = verify_ssl_request_proof(json.loads(ssl_path.read_text()), receipt)
                    result["ssl_request_proof_sha256"] = digest(ssl_path)
                if case == "read-cut":
                    cut_path = run_dir / "read-cut-proof.json"
                    result["read_cut"] = verify_read_cut_proof(json.loads(cut_path.read_text()), receipt)
                    result["read_cut_proof_sha256"] = digest(cut_path)
                if case == "bandwidth":
                    bandwidth_path = run_dir / "bandwidth-proof.json"
                    result["bandwidth"] = verify_bandwidth_proof(json.loads(bandwidth_path.read_text()), receipt)
                    result["bandwidth_proof_sha256"] = digest(bandwidth_path)
                if case.startswith("commit-"):
                    commit_path = run_dir / "commit-proof.json"
                    result["commit_loss"] = verify_commit_proof(json.loads(commit_path.read_text()), receipt, "drop" if case == "commit-loss" else case.removeprefix("commit-") + "-deadline")
                    result["commit_proof_sha256"] = digest(commit_path)
                if case == "auth-stall":
                    auth_path = run_dir / "auth-proof.json"
                    result["authentication"] = verify_auth_proof(json.loads(auth_path.read_text()), receipt)
                    result["auth_proof_sha256"] = digest(auth_path)
                require(all(previous["run_id"] != receipt["run_id"] for name, previous in summary["runs"].items() if name != case), "acceptance cases reused a run ID")
            finally:
                # Independent inspection also runs when receipt validation fails.
                try:
                    result["cleanup"] = verify_cleanup(api, receipt, project)
                finally:
                    if case == "auth-stall":
                        result.setdefault("cleanup", {})["authentication_backend"] = verify_auth_database(args.repo.resolve(), receipt, run_dir / "auth-proof.json")
                    if case.startswith("commit-"):
                        result.setdefault("cleanup", {})["commit_backend"] = verify_commit_database(args.repo.resolve(), receipt, run_dir / "commit-proof.json")
                    if case == "bandwidth":
                        result.setdefault("cleanup", {})["bandwidth_backend"] = verify_bandwidth_database(args.repo.resolve(), receipt, run_dir / "bandwidth-proof.json")
                    if case == "read-cut":
                        result.setdefault("cleanup", {})["read_cut_backend"] = verify_read_cut_database(args.repo.resolve(), receipt, run_dir / "read-cut-proof.json")
                    if case == "ssl-request":
                        result.setdefault("cleanup", {})["ssl_request_backend"] = verify_ssl_request_database(args.repo.resolve(), receipt, run_dir / "ssl-request-proof.json")
                    if case == "retry-budget":
                        result.setdefault("cleanup", {})["retry_budget_backend"] = verify_retry_budget_database(args.repo.resolve(), receipt, run_dir / "retry-budget-proof.json")
                    if case == "circuit-breaker":
                        result.setdefault("cleanup", {})["circuit_breaker_backend"] = verify_circuit_breaker_database(args.repo.resolve(), receipt, run_dir / "circuit-breaker-proof.json")
                    if case == "bulkhead":
                        result.setdefault("cleanup", {})["bulkhead_backend"] = verify_bulkhead_database(args.repo.resolve(), receipt, run_dir / "bulkhead-proof.json")
                    if case == "combined":
                        result.setdefault("cleanup", {})["combined_backend"] = verify_combined_database(args.repo.resolve(), receipt, run_dir / "combined-proof.json")
                    if case == "isolation":
                        result.setdefault("cleanup", {})["isolation_backend"] = verify_isolation_database(args.repo.resolve(), receipt, run_dir / "isolation-proof.json")
                    if case == "repeated":
                        result.setdefault("cleanup", {})["repeated_backend"] = verify_repeated_database(args.repo.resolve(), receipt, run_dir / "repeated-proof.json")
                    if case in DEPENDENCY_MATRIX:
                        result.setdefault("cleanup", {})["postgres_ledger"] = verify_dependency_database(args.repo.resolve(), receipt)
            result["verified"] = True
            print("native checkout " + case + ": verified, including cleanup", flush=True)
        require(set(summary["runs"]) == set(CASES) and all(run["verified"] and run["cleanup"]["passed"] for run in summary["runs"].values()), "all thirty-eight native cases and cleanup proofs are required")
        summary["status"] = "passed"
    except (OSError, ValueError, TypeError, KeyError, ET.ParseError, subprocess.SubprocessError, KeyboardInterrupt) as error:
        summary["reason"] = type(error).__name__ + ": " + str(error)
        raise SystemExit("native checkout acceptance did not pass: " + summary["reason"]) from None
    finally:
        (args.out / ("runner.json" if args.check_runner else "verdict.json")).write_text(json.dumps(summary, indent=2) + "\n")


if __name__ == "__main__":
    main()
