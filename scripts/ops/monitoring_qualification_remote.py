#!/usr/bin/env python3
"""Fixed-loopback operations for the manually dispatched qualification workflow.

Loaded over pinned SSH with credentials on stdin. No configuration/secret files
are read and no watchdog, silence, deployment or service state is modified.
"""

from datetime import datetime, timedelta, timezone
import hashlib
import json
import math
from pathlib import Path
import re
import sys
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener


ROLES = ("faas-heartbeat", "faas-page", "faas-warn")
INTEGRATIONS = ("email", "slack", "pushover", "webhook", "pagerduty", "opsgenie", "victorops", "wechat", "telegram", "webex", "sns", "discord", "msteams", "msteamsv2", "rocketchat", "jira")
MAX_BYTES = 1 << 20  # pkg/api/limits.go: ObsAlertmanagerResponseMaxBytes.
GROUPS_MAX = 32  # pkg/api/limits.go: ObsNotificationIntegrationsMax.
TOKEN_MAX = 4096
ALERT_LIFETIME_SECONDS = 900


class QualificationError(Exception):
    pass


def stamp(at):
    return at.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def timestamp(value):
    if not isinstance(value, str) or len(value) > 40:
        raise QualificationError("invalid_timestamp")
    try:
        at = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        raise QualificationError("invalid_timestamp") from None
    if at.tzinfo is None:
        raise QualificationError("invalid_timestamp")
    return at.astimezone(timezone.utc)


def fresh(value, now):
    at = timestamp(value)
    return now - timedelta(seconds=120) <= at <= now + timedelta(seconds=30)


def inventory_digest(groups):
    return hashlib.sha256(json.dumps(groups, separators=(",", ":"), sort_keys=True).encode()).hexdigest()


def receiver_inventory(body, now):
    if not isinstance(body, dict) or not fresh(body.get("generated_at"), now):
        raise QualificationError("inventory_unavailable")
    health = body.get("receiver_health")
    if not isinstance(health, dict):
        raise QualificationError("receiver_api_unavailable")
    for name in ("collector", "inventory"):
        signal = health.get(name)
        if not isinstance(signal, dict) or signal.get("state") != "healthy" or signal.get("reason", "") != "" or not fresh(signal.get("observed_at"), now):
            raise QualificationError("inventory_unavailable")
        value = signal.get("value")
        if type(value) not in (int, float) or not math.isfinite(value):
            raise QualificationError("inventory_unavailable")
    count = health["inventory"]["value"]
    if health["collector"]["value"] != 1 or count < 1 or count > GROUPS_MAX or count != int(count):
        raise QualificationError("inventory_unavailable")
    rows = health.get("groups")
    if not isinstance(rows, list) or len(rows) != count:
        raise QualificationError("inventory_unavailable")
    groups = []
    for row in rows:
        if not isinstance(row, dict) or row.get("expected") is not True or row.get("receiver") not in ROLES or row.get("integration") not in INTEGRATIONS:
            raise QualificationError("invalid_receiver_inventory")
        groups.append({"receiver": row["receiver"], "integration": row["integration"]})
    groups.sort(key=lambda row: (row["receiver"], row["integration"]))
    if len({(row["receiver"], row["integration"]) for row in groups}) != len(groups):
        raise QualificationError("invalid_receiver_inventory")
    roles = {row["receiver"] for row in groups}
    if not {"faas-page", "faas-heartbeat"}.issubset(roles):
        raise QualificationError("required_receiver_missing")
    if any(row["receiver"] == "faas-heartbeat" and row["integration"] != "webhook" for row in groups):
        raise QualificationError("invalid_receiver_inventory")
    return groups


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request_json(path, token="", alerts=None):
    # There is no destination parameter. The only mutating endpoint is this
    # loopback Alertmanager alert submission; redirects/proxies are disabled.
    if path == "inventory":
        request = Request("http://127.0.0.1:8080/v1/admin/obs/notification-health", headers={"Authorization": "Bearer " + token, "Accept": "application/json", "User-Agent": "gregale-monitoring-qualification/1"})
    elif path == "alerts" and alerts:
        request = Request("http://127.0.0.1:9094/api/v2/alerts", data=json.dumps(alerts).encode(), headers={"Content-Type": "application/json"}, method="POST")
    else:
        raise QualificationError("invalid_operation")
    try:
        with build_opener(ProxyHandler({}), NoRedirect()).open(request, timeout=5) as response:
            if response.status < 200 or response.status >= 300:
                raise QualificationError("source_unavailable")
            body = response.read(MAX_BYTES + 1)
            if len(body) > MAX_BYTES:
                raise QualificationError("response_too_large")
            return json.loads(body) if path == "inventory" else None
    except QualificationError:
        raise
    except (OSError, ValueError, RecursionError):
        raise QualificationError("source_unavailable") from None


def active_release():
    path = str(Path("/opt/faas/current").resolve())
    match = re.fullmatch(r"/opt/faas/releases/([0-9a-f]{40})", path)
    if match is None:
        raise QualificationError("active_release_unavailable")
    return match[1]


def test_alerts(plan, now, resolving=False):
    identity = plan.get("qualification_id", "")
    if not re.fullmatch(r"mq-[1-9][0-9]{0,19}-[1-9][0-9]{0,5}", identity):
        raise QualificationError("invalid_test_identity")
    started = timestamp(plan.get("alert_starts_at"))
    expires = timestamp(plan.get("alert_expires_at"))
    if expires - started != timedelta(seconds=ALERT_LIFETIME_SECONDS) or started > now + timedelta(seconds=30):
        raise QualificationError("invalid_alert_window")
    if not resolving and (now >= expires or now - started > timedelta(minutes=2)):
        raise QualificationError("alert_window_expired")
    groups = plan.get("expected_groups")
    if not isinstance(groups, list) or not 1 <= len(groups) <= GROUPS_MAX:
        raise QualificationError("invalid_receiver_inventory")
    roles = set()
    for row in groups:
        if not isinstance(row, dict) or row.get("receiver") not in ROLES or row.get("integration") not in INTEGRATIONS:
            raise QualificationError("invalid_receiver_inventory")
        roles.add(row["receiver"])
    alerts = []
    for role in ("faas-page", "faas-warn"):
        if role not in roles:
            continue
        severity = role.removeprefix("faas-")
        # Unique alertname separates page groups; distinct families avoid
        # page-over-warn inhibition and collision with real warn digests.
        labels = {"alertname": "FaasDeliveryQualification_" + identity.replace("-", "_") + "_" + severity,
                  "component": "alertmanager", "severity": severity,
                  "family": "monitoring_qualification_" + identity + "_" + severity,
                  "qualification_id": identity}
        alerts.append({"labels": labels, "annotations": {"summary": "Gregale delivery qualification " + identity + " " + severity,
                                                        "qualification_id": identity},
                       "startsAt": stamp(started), "endsAt": stamp(max(now, started) if resolving else expires)})
    if not alerts:
        raise QualificationError("required_receiver_missing")
    return alerts


def operate(payload, now=None, read_release=active_release, request=request_json):
    now = now or datetime.now(timezone.utc)
    if not isinstance(payload, dict) or payload.get("operation") not in ("prepare", "fire", "resolve"):
        raise QualificationError("invalid_operation")
    operation = payload["operation"]
    # Cleanup only resolves alerts whose entire identity is deterministically
    # regenerated from this plan. It still runs if monitoring/release changes.
    if operation == "resolve":
        request("alerts", alerts=test_alerts(payload, now, resolving=True))
        return {"operation": operation, "state": "accepted", "at": stamp(now)}
    token = payload.get("operator_token", "")
    if not isinstance(token, str) or not token.strip() or len(token.encode()) > TOKEN_MAX or any(char in token for char in "\r\n\x00"):
        raise QualificationError("operator_credential_unavailable")
    release = read_release()
    if payload.get("expected_release", release) != release:
        raise QualificationError("active_release_changed")
    groups = receiver_inventory(request("inventory", token=token), now)
    digest = inventory_digest(groups)
    if operation == "fire":
        if payload.get("inventory_sha256") != digest or payload.get("expected_groups") != groups:
            raise QualificationError("receiver_inventory_changed")
        request("alerts", alerts=test_alerts(payload, now))
    if read_release() != release:
        raise QualificationError("active_release_changed")
    return {"operation": operation, "state": "accepted", "at": stamp(now), "release": release,
            "expected_groups": groups, "inventory_sha256": digest}


def main():
    try:
        raw = sys.stdin.buffer.read(MAX_BYTES + 1)
        if len(raw) > MAX_BYTES:
            raise QualificationError("invalid_request")
        result = operate(json.loads(raw))
    except QualificationError as error:
        result = {"state": "unknown", "reason": str(error)}
    except (OSError, ValueError, TypeError, KeyError, OverflowError, RecursionError):
        result = {"state": "unknown", "reason": "source_unavailable"}
    print(json.dumps(result, allow_nan=False))
    return 0 if result["state"] == "accepted" else 3


if __name__ == "__main__":
    raise SystemExit(main())
