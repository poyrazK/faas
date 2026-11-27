#!/usr/bin/env python3
"""Local qualification of Prometheus -> Alertmanager -> webhook receipt.

Runs isolated binaries on random loopback ports. Reuses the production route
tree, with local webhook receivers and compressed timers. Never reads receiver
credentials or contacts deployed services. The PagerDuty adapter posts to a
mock endpoint. Provider escalation and human receipt require a live drill.
"""

import argparse
import copy
from datetime import datetime, timedelta, timezone
import http.server
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.parse
import urllib.request
import uuid

from monitoring_qualification_remote import ALERT_LIFETIME_SECONDS, DELIVERY_RECEIVER, stamp, test_alerts

ROOT = Path(__file__).resolve().parents[2]
RECEIVERS = ("faas-page", "faas-warn", "faas-heartbeat")
PAGERDUTY_KEY = "local-drill-key"
DELIVERY_ALERTS = ("FaasPrometheusNotificationsDropped", "FaasAlertmanagerNotificationsFailed")
RUNTIME_REPLICAS = ("127.0.0.1:9101", "127.0.0.2:9101")
RUNTIME_ALERT = "FaasRuntimeCollectorDeadlines"
RUNTIME_METRIC = 'apid_operator_runtime_reports_total{outcome="deadline_exceeded"}'
GRAFANA_BASE = "https://grafana.example.test/ops"
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def collector_module():
    path = ROOT / "deploy/ansible/roles/notification_receiver_metrics/files/faas-notification-receiver-metrics"
    loader = importlib.machinery.SourceFileLoader("drill_receiver_metrics", str(path))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


def get(url):
    with OPENER.open(url, timeout=2) as response:
        return response.read(1 << 20)


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def stop(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=3)


class Receiver:
    def __init__(self, run_id):
        self.run_id, self.stage, self.firing = run_id, "baseline", False
        self.reject_page, self.receipts = False, []
        self.runtime_failing = set()
        self.runtime_counts = dict.fromkeys(RUNTIME_REPLICAS, 0)
        self.chat_receipts = []
        self.pagerduty_receipts = []
        self.lock = threading.Lock()

    def seen(self, receiver, phase, stage=None, accepted=True):
        with self.lock:
            return any(r["receiver"] == receiver and r["phase"] == phase and r["accepted"] == accepted
                       and (stage is None or r["stage"] == stage) for r in self.receipts)

    def pagerduty_events(self):
        with self.lock:
            return self.pagerduty_receipts.copy()

    def latest_heartbeat_age(self):
        with self.lock:
            stamps = [r["monotonic"] for r in self.receipts if r["receiver"] == "faas-heartbeat" and r["accepted"]]
        return time.monotonic() - max(stamps) if stamps else 0

    def record_chat(self, receiver, payload):
        attachments = payload.get("attachments") if isinstance(payload, dict) else None
        if receiver not in RECEIVERS[:2] or not isinstance(attachments, list) or not attachments:
            raise ValueError("invalid chat payload")
        if any(not isinstance(attachment, dict) for attachment in attachments):
            raise ValueError("invalid chat attachment")
        text = "\n".join(attachment.get("text", "") for attachment in attachments)
        with self.lock:
            if len(self.chat_receipts) >= 512:
                raise ValueError("chat receipt bound exceeded")
            self.chat_receipts.append((receiver, text))

    def runtime_recoveries(self):
        with self.lock:
            return {r["instance"]: r["ends_at_ms"] for r in self.receipts
                    if r["receiver"] == "faas-warn" and r["accepted"] and "ends_at_ms" in r}

    def saw_runtime_chat(self, states):
        runbook = f"https://github.com/poyrazK/faas/blob/main/docs/runbooks/{RUNTIME_ALERT}.md"
        recoveries = self.runtime_recoveries()
        expected = set()
        for instance, state in states.items():
            start, end = "now-2h", "now"
            if state == "resolved":
                end = recoveries.get(instance)
                if end is None:
                    return False
                start = end - 7_200_000
            query = urllib.parse.urlencode({"var-apid_instance": instance, "from": start, "to": end})
            dashboard = GRAFANA_BASE + "/d/faas-runtime-diagnostics?" + query
            expected.add((f"{state.upper()} Runtime collections on {instance} repeatedly time out",
                          f"Runtime Diagnostics <{dashboard}> · Runbook <{runbook}>"))
        with self.lock:
            for receiver, text in self.chat_receipts:
                if receiver == "faas-warn":
                    lines = [line.strip() for line in text.splitlines() if line.strip()]
                    if len(lines) == 2 * len(expected) and set(zip(lines[::2], lines[1::2])) == expected:
                        return True
        return False

    def handler(self):
        owner = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                with owner.lock:
                    replica = self.path.removeprefix("/runtime/")
                    if replica in owner.runtime_counts:
                        if replica in owner.runtime_failing:
                            owner.runtime_counts[replica] += 2
                        body = f"{RUNTIME_METRIC} {owner.runtime_counts[replica]}\n".encode()
                    else:
                        body = f'gregale_notification_drill_firing{{stage="{owner.stage}"}} {int(owner.firing)}\n'.encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/plain; version=0.0.4")
                self.end_headers()
                self.wfile.write(body)

            def do_POST(self):
                try:
                    size = int(self.headers.get("Content-Length", "0"))
                    if not 0 < size <= 65536:
                        raise ValueError("invalid request")
                    payload = json.loads(self.rfile.read(size))
                    if self.path.startswith("/chat/"):
                        owner.record_chat(self.path.removeprefix("/chat/"), payload)
                        self.send_response(200)
                        self.send_header("Content-Type", "text/plain")
                        self.end_headers()
                        self.wfile.write(b"ok")
                        return
                    if self.path == "/pagerduty":
                        action, key = payload["event_action"], payload["dedup_key"]
                        if payload["routing_key"] != PAGERDUTY_KEY or action not in ("trigger", "resolve") or not key:
                            raise ValueError("invalid PagerDuty event")
                        with owner.lock:
                            owner.pagerduty_receipts.append((action, key, payload.get("payload", {})))
                        self.send_response(202)
                        self.end_headers()
                        return
                    receiver = self.path.removeprefix("/receive/")
                    if (receiver not in RECEIVERS or not isinstance(payload, dict) or payload.get("receiver") != receiver
                            or not isinstance(payload.get("alerts"), list) or not payload["alerts"]):
                        raise ValueError("invalid receiver")
                    rows = []
                    for alert in payload["alerts"]:
                        if not isinstance(alert, dict) or not isinstance(alert.get("annotations"), dict) or not isinstance(alert.get("labels"), dict):
                            raise ValueError("invalid alert")
                        if alert["annotations"].get("drill_id") != owner.run_id or alert.get("status") not in ("firing", "resolved"):
                            raise ValueError("unrelated alert")
                        at = time.time()
                        reference = alert["endsAt"] if alert["status"] == "resolved" else alert["startsAt"]
                        if not isinstance(reference, str):
                            raise ValueError("invalid event time")
                        event_at = datetime.fromisoformat(reference.replace("Z", "+00:00")).timestamp()
                        latency = max(0, at - event_at)
                        labels = alert["labels"]
                        stage = "runtime" if labels.get("alertname") == RUNTIME_ALERT else labels.get("stage", "watchdog")
                        row = {"receiver": receiver, "phase": alert["status"], "stage": stage,
                               "received_at": at, "receipt_latency_seconds": latency, "monotonic": time.monotonic()}
                        if stage == "runtime" and alert["status"] == "resolved":
                            row.update(instance=labels["instance"], ends_at_ms=int(event_at * 1000))
                        rows.append(row)
                    with owner.lock:
                        accepted = not (owner.reject_page and receiver == "faas-page")
                        if len(owner.receipts) + len(rows) > 512:
                            raise ValueError("receipt bound exceeded")
                        owner.receipts.extend(dict(row, accepted=accepted) for row in rows)
                    self.send_response(200 if accepted else 503)
                except (ValueError, KeyError, TypeError):
                    self.send_response(400)
                self.end_headers()

            def log_message(self, *args):
                pass

        return Handler


def render_config(directory, receiver_url, run_id):
    # These dependencies are supplied by the repository's pinned Ansible venv.
    import jinja2
    import yaml

    role = ROOT / "deploy/ansible/roles/alertmanager"
    values = yaml.safe_load((role / "defaults/main.yml").read_text())
    page_url, warn_url = directory / "page-url", directory / "warn-url"
    page_url.write_text(receiver_url + "/chat/faas-page\n")
    warn_url.write_text(receiver_url + "/chat/faas-warn\n")
    pagerduty_key = directory / "pagerduty-key"
    pagerduty_key.write_text(PAGERDUTY_KEY + "\n")
    values.update(am_delivery_pagerduty_key_file=str(pagerduty_key), am_fleet_name="local-drill",
                  am_slack_webhook_url_file=str(page_url), am_slack_warn_webhook_url_file=str(warn_url),
                  am_heartbeat_url_file="local-heartbeat-placeholder", am_heartbeat_interval="2s", am_grafana_base_url=GRAFANA_BASE + "/")
    env = jinja2.Environment(undefined=jinja2.StrictUndefined)
    env.filters.update(to_json=lambda value: json.dumps(value), bool=bool)
    config = yaml.safe_load(env.from_string((role / "templates/alertmanager.yml.j2").read_text()).render(**values))

    def compress(route):
        # Give the two independently scraped replicas time to join one warning group.
        group_wait = {"faas-warn": "3s", DELIVERY_RECEIVER: "1s"}.get(route["receiver"], "0s")
        route.update(group_wait=group_wait, group_interval="1s", repeat_interval="2s")
        for child in route.get("routes", []):
            compress(child)

    compress(config["route"])
    # Run the shipped chat and PagerDuty adapters against fixture endpoints.
    config["global"].pop("http_config", None)
    local_receivers = []
    for rendered in config["receivers"]:
        name = rendered["name"]
        receiver = {"name": name}
        if name in RECEIVERS:
            receiver["webhook_configs"] = [{"url": receiver_url + "/receive/" + name, "send_resolved": name != "faas-heartbeat"}]
        if name in RECEIVERS[:2] and "slack_configs" in rendered:
            for delivery in rendered["slack_configs"]:
                if delivery.get("api_url_file") not in (str(page_url), str(warn_url)) or "api_url" in delivery:
                    raise RuntimeError("chat receiver lacks a fixture URL file")
                delivery.pop("http_config", None)
            receiver["slack_configs"] = rendered["slack_configs"]
        if name == DELIVERY_RECEIVER:
            delivery, = rendered["pagerduty_configs"]
            if delivery.get("routing_key_file") != str(pagerduty_key) or "routing_key" in delivery:
                raise RuntimeError("PagerDuty receiver lacks a fixture key file")
            delivery.update(url=receiver_url + "/pagerduty")
            delivery.pop("http_config", None)
            receiver["pagerduty_configs"] = [delivery]
        local_receivers.append(receiver)
    config["receivers"] = local_receivers
    (directory / "alertmanager.yml").write_text(yaml.safe_dump(config, sort_keys=False))

    shipped = yaml.safe_load((ROOT / "deploy/ansible/roles/prometheus/files/faas.rules.yml").read_text())
    watchdog = copy.deepcopy(next(r for g in shipped["groups"] for r in g["rules"] if r.get("alert") == "FaasWatchdog"))
    watchdog.setdefault("annotations", {})["drill_id"] = run_id
    rules = [watchdog]
    runtime = copy.deepcopy(next(r for g in shipped["groups"] for r in g["rules"] if r.get("alert") == RUNTIME_ALERT))
    # Exercise the shipped expression, routing labels and text with a short local window.
    runtime["expr"] = re.sub(r"\[\d+[smhd]\]", "[6s]", runtime["expr"])
    runtime["for"] = "2s"
    runtime["annotations"]["drill_id"] = run_id
    rules.append(runtime)
    for severity in ("page", "warn"):
        rules.append({"alert": "FaasNotificationDrill" + severity.title(), "expr": "gregale_notification_drill_firing == 1",
                      "labels": {"severity": severity, "component": "platform", "family": "notification_drill_" + severity},
                      "annotations": {"drill_id": run_id, "summary": "Controlled local notification drill"}})
    (directory / "rules.yml").write_text(yaml.safe_dump({"groups": [{"name": "notification_drill", "interval": "1s", "rules": rules}]}))


def check_runtime_deadlines(receiver, prom_url, wait):
    def counters_ready():
        url = prom_url + "/api/v1/query?" + urllib.parse.urlencode({"query": RUNTIME_METRIC})
        rows = json.loads(get(url))["data"]["result"]
        return {row["metric"]["instance"]: float(row["value"][1]) for row in rows} == dict.fromkeys(RUNTIME_REPLICAS, 0)

    wait("runtime_deadline_counters_scraped", counters_ready)
    with receiver.lock:
        receiver.runtime_failing.update(RUNTIME_REPLICAS)
    wait("runtime_deadlines_grouped_with_links", lambda: receiver.saw_runtime_chat(dict.fromkeys(RUNTIME_REPLICAS, "firing")))

    first, second = RUNTIME_REPLICAS
    with receiver.lock:
        receiver.runtime_failing.remove(first)
    wait("runtime_first_replica_resolved_with_links",
         lambda: receiver.saw_runtime_chat({first: "resolved", second: "firing"}))
    with receiver.lock:
        receiver.runtime_failing.remove(second)
    wait("runtime_second_replica_resolved_with_links",
         lambda: receiver.saw_runtime_chat({second: "resolved"})
         or receiver.saw_runtime_chat(dict.fromkeys(RUNTIME_REPLICAS, "resolved")))
    if len(set(receiver.runtime_recoveries().values())) != len(RUNTIME_REPLICAS):
        raise RuntimeError("runtime_deadline_resolutions_lack_distinct_timestamps")
    if receiver.seen("faas-page", "firing", "runtime") or receiver.seen("faas-page", "resolved", "runtime"):
        raise RuntimeError("runtime_deadlines_reached_page_receiver")


def check_delivery_paging(receiver, am_url, run_id, wait):
    def submit(alerts):
        request = urllib.request.Request(am_url + "/api/v2/alerts", data=json.dumps(alerts).encode(),
                                         headers={"Content-Type": "application/json"})
        with OPENER.open(request, timeout=2):
            pass

    def triggers(count, summary_prefix):
        return [key for action, key, payload in receiver.pagerduty_events()
                if action == "trigger" and payload["summary"].startswith(summary_prefix)
                and payload["custom_details"]["num_firing"] == str(count)]

    now = datetime.now(timezone.utc)
    ends = now + timedelta(seconds=ALERT_LIFETIME_SECONDS)
    plan = {"qualification_id": "mq-123-1", "expected_groups": [{"receiver": DELIVERY_RECEIVER, "integration": "pagerduty"}],
            "alert_starts_at": stamp(now), "alert_expires_at": stamp(ends)}
    alerts = [{"labels": {"alertname": name, "severity": "page", "component": name, "stage": "delivery"},
               "annotations": {"drill_id": run_id, "summary": name, "first_action": "Check notification transport."},
               "startsAt": stamp(now), "endsAt": stamp(ends)} for name in DELIVERY_ALERTS]
    submit(alerts + test_alerts(plan, now))
    live_title = "Gregale alert delivery degraded"
    test_title = "[TEST] Gregale delivery qualification mq-123-1"
    wait("pagerduty_groups_delivery_faults", lambda: triggers(2, live_title))
    wait("pagerduty_isolates_qualification", lambda: triggers(1, test_title))
    wait("delivery_faults_also_reach_page_route", lambda: receiver.seen("faas-page", "firing", "delivery"))
    live_key, test_key = triggers(2, live_title)[0], triggers(1, test_title)[0]
    if live_key == test_key:
        raise RuntimeError("qualification_shared_live_dedup_key")

    resolved_at = datetime.now(timezone.utc)
    submit([dict(alerts[0], endsAt=stamp(resolved_at))] + test_alerts(plan, resolved_at, resolving=True))
    wait("pagerduty_keeps_incident_open_for_second_fault", lambda: live_key in triggers(1, live_title))
    wait("pagerduty_resolves_qualification", lambda: any(action == "resolve" and key == test_key
                                                         for action, key, _ in receiver.pagerduty_events()))
    if any(action == "resolve" and key == live_key for action, key, _ in receiver.pagerduty_events()):
        raise RuntimeError("pagerduty_resolved_while_one_fault_remained")

    submit([dict(alerts[1], endsAt=stamp(datetime.now(timezone.utc)))])
    wait("pagerduty_resolves_combined_incident", lambda: any(action == "resolve" and key == live_key
                                                           for action, key, _ in receiver.pagerduty_events()))


def run(prometheus, alertmanager, timeout=90):
    import yaml

    run_id = uuid.uuid4().hex
    receiver = Receiver(run_id)
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), receiver.handler())
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    checks, processes = {}, []
    deadline = time.monotonic() + timeout

    def wait(name, predicate, budget=15):
        end = min(deadline, time.monotonic() + budget)
        while time.monotonic() < end:
            if any(p.poll() is not None for p in processes):
                raise RuntimeError("isolated_process_exited")
            try:
                if predicate():
                    checks[name] = True
                    print("notification-drill: " + name + ": PASS", file=sys.stderr, flush=True)
                    return
            except (OSError, ValueError, json.JSONDecodeError):
                pass
            time.sleep(0.1)
        # Logs describe only disposable loopback processes and contain no
        # configured receiver credentials. Keep CI failures diagnosable.
        for log in directory.glob("*.log"):
            print(log.name + ":\n" + log.read_text()[-4000:], file=sys.stderr)
        with receiver.lock:
            print("receipt states: " + json.dumps(receiver.receipts[-6:]), file=sys.stderr)
            print("chat receipts: " + json.dumps(receiver.chat_receipts[-6:]), file=sys.stderr)
        raise RuntimeError("deadline_exceeded:" + name)

    try:
        with tempfile.TemporaryDirectory(prefix="gregale-notification-drill-") as work:
            directory = Path(work)
            receiver_url = f"http://127.0.0.1:{server.server_port}"
            am_url, prom_url = f"http://127.0.0.1:{free_port()}", f"http://127.0.0.1:{free_port()}"
            render_config(directory, receiver_url, run_id)
            scrape_target = receiver_url.removeprefix("http://")
            config = {"global": {"scrape_interval": "1s", "evaluation_interval": "1s"},
                      "rule_files": [str(directory / "rules.yml")],
                      "alerting": {"alertmanagers": [{"static_configs": [{"targets": [am_url.removeprefix("http://")]}]}]},
                      "scrape_configs": [
                          {"job_name": "drill", "static_configs": [{"targets": [scrape_target]}]},
                          {"job_name": "apid", "static_configs": [
                              {"targets": [scrape_target], "labels": {"instance": instance, "__metrics_path__": "/runtime/" + instance}}
                              for instance in RUNTIME_REPLICAS
                          ]},
                      ]}
            (directory / "prometheus.yml").write_text(yaml.safe_dump(config))
            with (directory / "alertmanager.log").open("w") as am_log, (directory / "prometheus.log").open("w") as prom_log:
                am = subprocess.Popen([alertmanager, "--config.file=" + str(directory / "alertmanager.yml"),
                    "--storage.path=" + str(directory / "am-data"), "--web.listen-address=" + am_url.removeprefix("http://"),
                    "--cluster.listen-address=", "--enable-feature=receiver-name-in-metrics"], stdout=am_log, stderr=am_log)
                processes.append(am)
                wait("alertmanager_ready", lambda: get(am_url + "/-/ready"))
                collector = collector_module()
                snapshot = lambda: collector.parse(get(am_url + "/metrics").decode())
                baseline = collector.observe(snapshot(), {}, time.time())
                prom = subprocess.Popen([prometheus, "--config.file=" + str(directory / "prometheus.yml"),
                    "--storage.tsdb.path=" + str(directory / "prom-data"), "--web.listen-address=" + prom_url.removeprefix("http://"),
                    "--rules.alert.resend-delay=1s"], stdout=prom_log, stderr=prom_log)
                processes.append(prom)
                wait("prometheus_ready", lambda: get(prom_url + "/-/ready"))
                with receiver.lock:
                    receiver.firing = True
                wait("firing_receipts", lambda: all(receiver.seen(name, "firing", "baseline") for name in RECEIVERS[:2]))
                wait("watchdog_receipt", lambda: receiver.seen("faas-heartbeat", "firing"))
                first = collector.observe(snapshot(), baseline, time.time())
                confirmed = collector.observe(snapshot(), first, time.time())
                if not all(confirmed["pairs"][name + "/webhook"]["last_success"] > 0 for name in RECEIVERS):
                    raise RuntimeError("successful_requests_not_observed")
                checks["receiver_observations"] = True
                with receiver.lock:
                    receiver.firing = False
                wait("recovery_receipts", lambda: all(receiver.seen(name, "resolved", "baseline") for name in RECEIVERS[:2]))
                with receiver.lock:
                    receiver.stage, receiver.firing, receiver.reject_page = "failure", True, True
                wait("receiver_failure_injected", lambda: receiver.seen("faas-page", "firing", "failure", accepted=False))
                wait("receiver_failure_isolated", lambda: receiver.seen("faas-warn", "firing", "failure"))
                wait("failed_transport_observed", lambda: snapshot()["pairs"]["faas-page/webhook"]["failures"] > 0)
                if receiver.seen("faas-page", "firing", "failure"):
                    raise RuntimeError("rejected_notification_counted_as_receipt")
                with receiver.lock:
                    receiver.reject_page, receiver.stage = False, "recovery"
                wait("receiver_recovered", lambda: all(receiver.seen(name, "firing", "recovery") for name in RECEIVERS[:2]))
                with receiver.lock:
                    receiver.firing = False
                wait("final_resolves", lambda: all(receiver.seen(name, "resolved", "recovery") for name in RECEIVERS[:2]))
                check_runtime_deadlines(receiver, prom_url, wait)
                check_delivery_paging(receiver, am_url, run_id, wait)
                final = snapshot()
                if final["pairs"][DELIVERY_RECEIVER + "/pagerduty"]["requests"] < 1:
                    raise RuntimeError("pagerduty_requests_not_observed")
                observations = collector.observe(final, confirmed, time.time())
                observations = collector.observe(final, observations, time.time())
                stop(prom)
                stop(am)
                processes.clear()
                wait("watchdog_deadline_detects_control_plane_loss", lambda: receiver.latest_heartbeat_age() >= 3)
                if not get(receiver_url + "/health"):
                    raise RuntimeError("receiver_not_independent")
                report = {"schema_version": 1, "scope": "local_webhook_routing_drill", "run_id": run_id, "passed": True,
                          "checks": checks, "human_acknowledgement": "not_assessed", "production_providers": "not_exercised",
                          "external_watchdog": "local_receipt_and_missed_deadline_verified", "receivers": list(observations["pairs"].values())}
                with receiver.lock:
                    report["receipts"] = [{k: v for k, v in row.items() if k != "monotonic"} for row in receiver.receipts]
                return report
    finally:
        for process in reversed(processes):
            stop(process)
        server.shutdown()
        server.server_close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prometheus", default=os.environ.get("PROMETHEUS") or shutil.which("prometheus"))
    parser.add_argument("--alertmanager", default=os.environ.get("ALERTMANAGER") or shutil.which("alertmanager"))
    parser.add_argument("--timeout", type=float, default=90)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if not args.prometheus or not args.alertmanager or not 0 < args.timeout <= 180:
        parser.error("pinned prometheus and alertmanager binaries and a timeout within 180s are required")
    try:
        report = run(args.prometheus, args.alertmanager, args.timeout)
    except (OSError, ValueError, RuntimeError, ImportError) as err:
        print("notification-drill: failed: " + str(err), file=sys.stderr)
        return 1
    body = json.dumps(report, indent=2) + "\n"
    if args.output:
        args.output.write_text(body)
        os.chmod(args.output, 0o600)
    else:
        print(body, end="")
    return 0


if __name__ == "__main__":
    sys.exit(main())
