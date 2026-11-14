#!/usr/bin/env python3
"""Local qualification of Prometheus -> Alertmanager -> webhook receipt.

Runs isolated binaries on random loopback ports. Reuses the production route
tree, with local webhook receivers and compressed timers. Never reads receiver
credentials or contacts deployed services. Provider adapters and human receipt
remain outside this local qualification.
"""

import argparse
import copy
from datetime import datetime
import http.server
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
RECEIVERS = ("faas-page", "faas-warn", "faas-heartbeat")
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
        self.lock = threading.Lock()

    def seen(self, receiver, phase, stage=None, accepted=True):
        with self.lock:
            return any(r["receiver"] == receiver and r["phase"] == phase and r["accepted"] == accepted
                       and (stage is None or r["stage"] == stage) for r in self.receipts)

    def latest_heartbeat_age(self):
        with self.lock:
            stamps = [r["monotonic"] for r in self.receipts if r["receiver"] == "faas-heartbeat" and r["accepted"]]
        return time.monotonic() - max(stamps) if stamps else 0

    def handler(self):
        owner = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                with owner.lock:
                    body = f'gregale_notification_drill_firing{{stage="{owner.stage}"}} {int(owner.firing)}\n'.encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/plain; version=0.0.4")
                self.end_headers()
                self.wfile.write(body)

            def do_POST(self):
                try:
                    receiver = self.path.removeprefix("/receive/")
                    size = int(self.headers.get("Content-Length", "0"))
                    if receiver not in RECEIVERS or not 0 < size <= 65536:
                        raise ValueError("invalid request")
                    payload = json.loads(self.rfile.read(size))
                    if not isinstance(payload, dict) or payload.get("receiver") != receiver or not isinstance(payload.get("alerts"), list) or not payload["alerts"]:
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
                        latency = max(0, at - datetime.fromisoformat(reference.replace("Z", "+00:00")).timestamp())
                        rows.append({"receiver": receiver, "phase": alert["status"], "stage": alert["labels"].get("stage", "watchdog"),
                                     "received_at": at, "receipt_latency_seconds": latency, "monotonic": time.monotonic()})
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
    values.update(am_slack_webhook_url_file="local-page-placeholder", am_slack_warn_webhook_url_file="local-warn-placeholder",
                  am_heartbeat_url_file="local-heartbeat-placeholder", am_heartbeat_interval="2s")
    env = jinja2.Environment(undefined=jinja2.StrictUndefined)
    env.filters.update(to_json=lambda value: json.dumps(value), bool=bool)
    config = yaml.safe_load(env.from_string((role / "templates/alertmanager.yml.j2").read_text()).render(**values))

    def compress(route):
        route.update(group_wait="0s", group_interval="1s", repeat_interval="2s")
        for child in route.get("routes", []):
            compress(child)

    compress(config["route"])
    config["receivers"] = [{"name": name, "webhook_configs": [{"url": receiver_url + "/receive/" + name,
                                 "send_resolved": name != "faas-heartbeat"}]} for name in RECEIVERS] + [{"name": "faas-silent"}]
    (directory / "alertmanager.yml").write_text(yaml.safe_dump(config, sort_keys=False))

    shipped = yaml.safe_load((ROOT / "deploy/ansible/roles/prometheus/files/faas.rules.yml").read_text())
    watchdog = copy.deepcopy(next(r for g in shipped["groups"] for r in g["rules"] if r.get("alert") == "FaasWatchdog"))
    watchdog.setdefault("annotations", {})["drill_id"] = run_id
    rules = [watchdog]
    for severity in ("page", "warn"):
        rules.append({"alert": "FaasNotificationDrill" + severity.title(), "expr": "gregale_notification_drill_firing == 1",
                      "labels": {"severity": severity, "component": "platform", "family": "notification_drill_" + severity},
                      "annotations": {"drill_id": run_id, "summary": "Controlled local notification drill"}})
    (directory / "rules.yml").write_text(yaml.safe_dump({"groups": [{"name": "notification_drill", "interval": "1s", "rules": rules}]}))


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
        raise RuntimeError("deadline_exceeded:" + name)

    try:
        with tempfile.TemporaryDirectory(prefix="gregale-notification-drill-") as work:
            directory = Path(work)
            receiver_url = f"http://127.0.0.1:{server.server_port}"
            am_url, prom_url = f"http://127.0.0.1:{free_port()}", f"http://127.0.0.1:{free_port()}"
            render_config(directory, receiver_url, run_id)
            config = {"global": {"scrape_interval": "1s", "evaluation_interval": "1s"},
                      "rule_files": [str(directory / "rules.yml")],
                      "alerting": {"alertmanagers": [{"static_configs": [{"targets": [am_url.removeprefix("http://")]}]}]},
                      "scrape_configs": [{"job_name": "drill", "static_configs": [{"targets": [receiver_url.removeprefix("http://")]}]}]}
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
                if not all(row["last_success"] > 0 for row in confirmed["pairs"].values()):
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
                final = snapshot()
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
