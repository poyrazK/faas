"""Validated Python deadline-only checkpoints and exact alert acknowledgments."""

from __future__ import annotations

from copy import deepcopy
from typing import Any

from ._event_deadline_policy import (
    MAX_COUNT,
    QUEUE_MAX,
    EventDeadlinePolicyOptions,
    EventDeadlineRatioOptions,
    EventDeadlineTarget,
    count,
    deadline_boundaries,
    policy_dict,
    target_dict,
    timestamp,
    uuid,
    valid_deadline_value,
)

KIND = "gregale.python.event-deadline-watch"


def _invalid() -> None:
    raise ValueError("invalid Python deadline watch checkpoint")


def _opening(value: Any, session: str) -> int:
    if not isinstance(value, str) or not value.startswith(session + "-"):
        _invalid()
    raw = value[len(session) + 1 :]
    if not raw.isascii() or not raw.isdecimal() or len(raw) > 16:
        _invalid()
    n = int(raw)
    if not count(n) or n == 0 or str(n) != raw:
        _invalid()
    return n


def validate_event_deadline_checkpoint(value: Any) -> dict[str, Any]:
    """Return detached canonical state. Stored observations/timers are not live proof."""
    if (
        not isinstance(value, dict)
        or value.get("kind") != KIND
        or type(value.get("version")) is not int
        or value["version"] not in (1, 2)
        or not uuid(value.get("session"))
        or not count(value.get("sequence"))
        or not isinstance(value.get("target"), dict)
        or not isinstance(value.get("policy"), dict)
    ):
        _invalid()
    t, p = value["target"], value["policy"]
    target = target_dict(EventDeadlineTarget(t.get("app"), t.get("app_id"), t.get("subscription_id")))
    if not isinstance(p.get("rules"), list) or not 1 <= len(p["rules"]) <= (30 if value["version"] == 1 else 31):
        _invalid()
    settings: dict[str, Any] = {
        "within_ms": p.get("within_ms"),
        "sustain_ms": p.get("sustain_ms"),
        "recovery_sustain_ms": p.get("recovery_sustain_ms"),
    }
    for rule in p["rules"]:
        if (
            not isinstance(rule, dict)
            or rule.get("cohort") not in ("upcoming", "expired")
            or not count(rule.get("threshold"))
            or rule["threshold"] == 0
            or not count(rule.get("recovery_threshold"))
            or rule["recovery_threshold"] >= rule["threshold"]
        ):
            _invalid()
        cohort, reason = rule["cohort"], rule.get("reason")
        if "minimum_recipients" in rule:
            if (
                value["version"] != 2
                or cohort != "expired"
                or "reason" in rule
                or "expired_ratio" in settings
                or not count(rule["minimum_recipients"])
                or rule["minimum_recipients"] < 1
            ):
                _invalid()
            settings["expired_ratio"] = EventDeadlineRatioOptions(
                warning_basis_points=rule["threshold"],
                recovery_basis_points=rule["recovery_threshold"],
                minimum_recipients=rule["minimum_recipients"],
            )
        elif reason is None:
            if "reason" in rule or cohort in settings:
                _invalid()
            settings[cohort], settings[cohort + "_recovery"] = rule["threshold"], rule["recovery_threshold"]
        else:
            if not isinstance(reason, str):
                _invalid()
            warnings = settings.setdefault(cohort + "_reasons", {})
            recoveries = settings.setdefault(cohort + "_reasons_recovery", {})
            if reason in warnings:
                _invalid()
            warnings[reason], recoveries[reason] = rule["threshold"], rule["recovery_threshold"]
    policy = policy_dict(EventDeadlinePolicyOptions(**settings))
    if (value["version"] == 2) != any("minimum_recipients" in r for r in policy["rules"]):
        _invalid()
    fields = ("metric", "cohort", "reason", "threshold", "recovery_threshold", "minimum_recipients")
    if any(any(a.get(k) != b.get(k) for k in fields) for a, b in zip(p["rules"], policy["rules"], strict=True)):
        _invalid()
    conditions, pending, acknowledged = value.get("conditions"), value.get("pending"), value.get("acknowledged")
    if (
        not isinstance(conditions, list)
        or len(conditions) != len(policy["rules"])
        or not isinstance(pending, list)
        or len(pending) > QUEUE_MAX
        or not isinstance(acknowledged, list)
        or len(acknowledged) > QUEUE_MAX
    ):
        _invalid()
    active: dict[str, str] = {}
    normalized_conditions = []
    for c, rule in zip(conditions, policy["rules"], strict=True):
        if (
            not isinstance(c, dict)
            or any(c.get(k) != rule.get(k) for k in fields)
            or c.get("state") not in ("unknown", "clear", "firing")
            or (("minimum_recipients" in c) != ("minimum_recipients" in rule))
            or ("minimum_recipients" in c and not count(c["minimum_recipients"]))
            or ("ratio_sample" in c and ("minimum_recipients" not in rule or "value" not in c))
            or ("value" in c and not valid_deadline_value(rule, c["value"], c.get("ratio_sample")))
            or ("observed_at" in c and timestamp(c["observed_at"]) is None)
            or ("value" in c) != ("observed_at" in c)
        ):
            _invalid()
        if "incident_id" in c:
            if (
                _opening(c["incident_id"], value["session"]) > value["sequence"]
                or c["incident_id"] in active
                or c["state"] == "clear"
                or "observed_at" not in c
            ):
                _invalid()
            active[c["incident_id"]] = c["metric"]
        elif c["state"] == "firing":
            _invalid()
        normalized = {**rule, **{k: c[k] for k in ("state", "value", "observed_at", "incident_id") if k in c}}
        if "ratio_sample" in c:
            normalized["ratio_sample"] = {
                k: c["ratio_sample"][k] for k in ("expired_recipients", "deadline_recipients")
            }
        normalized_conditions.append(normalized)
    if len(pending) + len(active) > QUEUE_MAX or value["sequence"] > MAX_COUNT - len(active):
        _invalid()
    seen: set[int] = set()
    lineage: dict[str, dict[str, Any]] = {}
    normalized_alerts = []
    rules = {r["metric"]: r for r in policy["rules"]}
    for rows in (pending, acknowledged):
        last, normalized = 0, []
        for a in rows:
            if (
                not isinstance(a, dict)
                or not count(a.get("sequence"))
                or not last < a["sequence"] <= value["sequence"]
                or a["sequence"] in seen
                or timestamp(a.get("at")) is None
                or a.get("transition") not in ("opened", "resolved")
                or not isinstance(a.get("metric"), str)
            ):
                _invalid()
            rule, opening = rules.get(a["metric"]), _opening(a.get("incident_id"), value["session"])
            if (
                rule is None
                or ("ratio_sample" in a and "minimum_recipients" not in rule)
                or not valid_deadline_value(rule, a.get("value"), a.get("ratio_sample"))
                or not count(a.get("threshold"))
                or not count(a.get("recovery_threshold"))
                or a["threshold"] != rule["threshold"]
                or a["recovery_threshold"] != rule["recovery_threshold"]
                or opening > a["sequence"]
            ):
                _invalid()
            breached, recovered = deadline_boundaries(
                {**rule, "value": a["value"], **({"ratio_sample": a["ratio_sample"]} if "ratio_sample" in a else {})}
            )
            if (a["transition"] == "opened" and (opening != a["sequence"] or not breached)) or (
                a["transition"] == "resolved" and (opening == a["sequence"] or not recovered)
            ):
                _invalid()
            row = lineage.setdefault(a["incident_id"], {"metric": a["metric"]})
            if row["metric"] != a["metric"] or a["transition"] in row:
                _invalid()
            row[a["transition"]] = a["sequence"]
            seen.add(a["sequence"])
            last = a["sequence"]
            normalized.append(
                {
                    k: a[k]
                    for k in (
                        "sequence",
                        "incident_id",
                        "metric",
                        "transition",
                        "at",
                        "value",
                        "threshold",
                        "recovery_threshold",
                    )
                }
            )
            if "ratio_sample" in a:
                normalized[-1]["ratio_sample"] = {
                    k: a["ratio_sample"][k] for k in ("expired_recipients", "deadline_recipients")
                }
        normalized_alerts.append(normalized)
    for incident, row in lineage.items():
        if (
            ("opened" in row and "resolved" in row and row["resolved"] <= row["opened"])
            or ("resolved" in row and incident in active)
            or (incident in active and active[incident] != row["metric"])
            or ("opened" in row and "resolved" not in row and incident not in active)
        ):
            _invalid()
    for incident in lineage.keys() | active.keys():
        opening = _opening(incident, value["session"])
        if opening in seen and lineage.get(incident, {}).get("opened") != opening:
            _invalid()
    return deepcopy(
        {
            "kind": KIND,
            "version": value["version"],
            "target": target,
            "policy": policy,
            "session": value["session"],
            "sequence": value["sequence"],
            "conditions": normalized_conditions,
            "pending": normalized_alerts[0],
            "acknowledged": normalized_alerts[1],
        }
    )


def acknowledge_event_deadline_alerts(checkpoint: Any, acknowledgements: list[dict[str, Any]]) -> dict[str, Any]:
    """All-or-nothing exact-pair acknowledgments; latest 1024 receipts remain idempotent."""
    out = validate_event_deadline_checkpoint(checkpoint)
    if not isinstance(acknowledgements, list) or len(acknowledgements) > QUEUE_MAX:
        raise ValueError("invalid deadline acknowledgments")
    keys: set[int] = set()
    known = {a["sequence"]: a["incident_id"] for a in out["pending"] + out["acknowledged"]}
    for ack in acknowledgements:
        if (
            not isinstance(ack, dict)
            or not count(ack.get("sequence"))
            or ack["sequence"] in keys
            or ack["sequence"] not in known
            or known[ack["sequence"]] != ack.get("incident_id")
        ):
            raise ValueError("unknown, duplicate or mismatched deadline acknowledgment")
        keys.add(ack["sequence"])
    for a in out["pending"]:
        if (
            a["sequence"] in keys
            and a["transition"] == "resolved"
            and any(
                o["incident_id"] == a["incident_id"] and o["transition"] == "opened" and o["sequence"] not in keys
                for o in out["pending"]
            )
        ):
            raise ValueError("acknowledge opening before or together with resolution")
    out["acknowledged"] = sorted(
        out["acknowledged"] + [a for a in out["pending"] if a["sequence"] in keys], key=lambda a: a["sequence"]
    )[-QUEUE_MAX:]
    out["pending"] = [a for a in out["pending"] if a["sequence"] not in keys]
    return out
