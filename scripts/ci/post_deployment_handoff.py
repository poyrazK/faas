"""Closed projection of imaged handoff observations; replica scopes may overlap."""

import datetime as dt
import post_deployment_diagnostics as diagnostics
import json
import re

COUNTS = ("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected",
          "waiting_scopes", "empty_scopes", "unsuccessful_collections", "working_recovery_scopes",
          "waiting_recovery_scopes", "stopping_recovery_scopes", "not_started_recovery_scopes")
QUALITY = ("complete", "missing", "stale", "conflicting", "unavailable")
RECOVERY = ("waiting", "working", "stopping", "not_started")
CLOCKS = ("first_waiting_at", "last_waiting_at", "first_empty_at", "last_empty_at")
# Mirrors the exact-integer wire safety bound in pkg/api/limits.go.
PENDING_MAX = (1 << 53)-1
REASONS = {
    "missing": ("expected_target_missing", "metric_missing"),
    "stale": ("scrape_stale", "metric_stale"),
    "conflicting": ("conflicting_targets", "conflicting_series", "conflicting_sample_times"),
    "unavailable": ("source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "metric_before_boundary", "invalid_value"),
}


def project_summary(raw, base):
    from post_deployment_builds import require, integer, SAMPLES_MAX, NODES_MAX
    require(isinstance(raw, dict) and isinstance(raw.get("counts"), dict))
    require(base["expected_role"] in ("single-box-v2", "control-plane-v2"))
    jobs = [job for job in base["jobs"] if job["job"] == "imaged" and job["daemon"] == "imaged"]
    require(len(jobs) == 1)
    maximum = SAMPLES_MAX+NODES_MAX
    c = {name: integer(raw["counts"].get(name), maximum) for name in COUNTS}
    total, backlog = sum(c[name] for name in QUALITY), c["waiting_scopes"]+c["empty_scopes"]
    recovery = sum(c[name+"_recovery_scopes"] for name in RECOVERY)
    require(c["expected"] == jobs[0]["expected"] and c["expected"] <= total <= maximum)
    require(c["observed"] <= min(total, base["counts"]["observed"]))
    require(c["complete"] <= min(backlog, recovery) and backlog+c["unsuccessful_collections"] <= c["observed"])
    require(recovery <= c["observed"] and c["unexpected"] <= c["observed"])
    incomplete = jobs[0]["state"] != "available" or any(c[name] for name in ("missing", "stale", "conflicting", "unavailable", "unexpected"))
    state, reason = ("waiting", "pending_handoffs_observed") if c["waiting_scopes"] else ("incomplete", "incomplete_handoff_evidence") if incomplete else ("empty", "") if c["complete"] else ("unknown", "handoff_evidence_unavailable")
    require(type(raw.get("incomplete")) is bool and raw["incomplete"] == incomplete)
    require(raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"state": state, "counts": c, "incomplete": incomplete}
    if "reason" in raw:
        out["reason"] = reason
    return out


def project_item(raw, base):
    from post_deployment_builds import require, integer, timestamp, MAX_AGE_SECONDS, CLOCK_SKEW_SECONDS, NODE_MAX_BYTES
    from post_deployment_process import number, UPTIME_MAX_SECONDS
    require(isinstance(raw, dict))
    generated, after = timestamp(base["generated_at"]), timestamp(base["evidence_after"])
    skew = dt.timedelta(seconds=CLOCK_SKEW_SECONDS)
    out = {"state": raw.get("state")}
    for name, pattern, maximum in (("node", r"[A-Za-z0-9][A-Za-z0-9_.-]*", NODE_MAX_BYTES), ("target_id", r"[0-9a-f]{64}", 64)):
        if name in raw:
            value = raw[name]
            require(isinstance(value, str) and len(value.encode()) <= maximum and (not value or re.fullmatch(pattern, value)))
            out[name] = value
    if "unexpected" in raw:
        require(type(raw["unexpected"]) is bool and (not raw["unexpected"] or bool(out.get("target_id"))))
        out["unexpected"] = raw["unexpected"]
    scrape = None
    if raw.get("scrape_observed_at") is not None:
        scrape = timestamp(raw["scrape_observed_at"])
        require(scrape <= generated+skew)
        out["scrape_observed_at"] = raw["scrape_observed_at"]

    def fresh(at):
        return at is not None and max(generated-dt.timedelta(seconds=MAX_AGE_SECONDS), after-skew) <= at <= generated+skew

    def origin(value, observed):
        if value is None:
            return None
        at = timestamp(value)
        require(generated-dt.timedelta(seconds=UPTIME_MAX_SECONDS) <= at <= generated+skew)
        require(at <= observed+2*skew)
        return at

    def component(value, valid, backlog=False):
        require(isinstance(value, dict))
        state, reason = value.get("state"), value.get("reason", "")
        result = {"state": state}
        if "reason" in value:
            result["reason"] = reason
        if state in valid:
            require(reason == "" and isinstance(value.get("values"), dict) and out.get("target_id") and fresh(scrape))
            require(fresh(timestamp(value.get("observed_at"))))
            result["observed_at"] = value["observed_at"]
        else:
            reasons = dict(REASONS)
            if backlog:
                reasons["stale"] += ("collection_stale",)
                reasons["unavailable"] += ("collection_failed", "collection_not_observed", "collection_before_boundary")
            require(isinstance(state, str) and state in reasons and reason in reasons[state])
            require(value.get("values") is None and value.get("observed_at") is None)
        return result

    collection = component(raw.get("collection"), ("available",))
    source_at, prerequisite = None, None
    if collection["state"] == "available":
        values = raw["collection"]["values"]
        require(type(values.get("success")) is bool)
        source_at = origin(values.get("last_success_at"), timestamp(collection["observed_at"]))
        require(not values["success"] or source_at is not None)
        collection["values"] = {"success": values["success"]}
        if source_at is not None:
            collection["values"]["last_success_at"] = values["last_success_at"]
        if not values["success"]:
            prerequisite = ("unavailable", "collection_failed" if source_at is not None else "collection_not_observed")
        elif source_at < generated-dt.timedelta(seconds=MAX_AGE_SECONDS):
            prerequisite = ("stale", "collection_stale")
        elif source_at < after-skew:
            prerequisite = ("unavailable", "collection_before_boundary")
    else:
        prerequisite = (collection["state"], collection.get("reason", ""))
    out["collection"] = collection

    backlog = component(raw.get("backlog"), ("waiting", "empty"), True)
    if prerequisite is not None:
        require(backlog["state"] == prerequisite[0] and backlog.get("reason", "") == prerequisite[1] and "observed_at" not in backlog)
    else:
        require(backlog.get("reason", "") not in ("collection_failed", "collection_not_observed", "collection_stale", "collection_before_boundary"))
    if backlog["state"] in ("waiting", "empty"):
        require(prerequisite is None and timestamp(backlog["observed_at"]) == timestamp(collection["observed_at"]))
        values = raw["backlog"]["values"]
        pending, age = integer(values.get("pending"), PENDING_MAX), number(values.get("oldest_wait_seconds"))
        oldest = origin(values.get("oldest_finished_at"), timestamp(backlog["observed_at"]))
        require((backlog["state"] == "waiting") == (pending > 0))
        if pending:
            require(oldest is not None and abs(age-max((generated-oldest).total_seconds(), 0)) <= 0.000001)
        else:
            require(oldest is None and age == 0)
        backlog["values"] = {"pending": pending, "oldest_wait_seconds": age}
        if oldest is not None:
            backlog["values"]["oldest_finished_at"] = values["oldest_finished_at"]
    out["backlog"] = backlog

    recovery = component(raw.get("recovery"), RECOVERY)
    if recovery["state"] in RECOVERY:
        values = raw["recovery"]["values"]
        require(type(values.get("enabled")) is bool and type(values.get("inflight")) is bool)
        observed = timestamp(recovery["observed_at"])
        attempt, completed = origin(values.get("last_attempt_at"), observed), origin(values.get("last_completed_at"), observed)
        require(completed is None or attempt is not None)
        if values["inflight"]:
            require(attempt is not None and (completed is None or attempt >= completed))
        if not values["enabled"]:
            state = "stopping"
        elif values["inflight"]:
            state = "working"
        elif attempt is None:
            state = "not_started"
        else:
            require(completed is not None and completed >= attempt)
            state = "waiting"
        require(recovery["state"] == state)
        recovery["values"] = {"enabled": values["enabled"], "inflight": values["inflight"]}
        for name in ("last_attempt_at", "last_completed_at"):
            if values.get(name) is not None:
                recovery["values"][name] = values[name]
    out["recovery"] = recovery
    expected = next((value["state"] for value in (collection, backlog, recovery) if value["state"] in REASONS), "available")
    require(out["state"] == expected)
    return out


def project(raw, base):
    from post_deployment_builds import require, integer, PREVIEW_MAX, SAMPLES_MAX, NODES_MAX
    out = project_summary(raw, base)
    require(isinstance(raw.get("items"), list) and len(raw["items"]) <= PREVIEW_MAX)
    out["items"] = [project_item(item, base) for item in raw["items"]]
    out["omitted_items"] = integer(raw.get("omitted_items"), SAMPLES_MAX+NODES_MAX)
    require(sum(out["counts"][name] for name in QUALITY) == len(out["items"])+out["omitted_items"])
    keys = [item.get("target_id") or "missing/"+item.get("node", "") for item in out["items"]]
    require(len(keys) == len(set(keys)))
    counts = dict.fromkeys(COUNTS, 0)
    for item in out["items"]:
        counts["observed"] += bool(item.get("target_id"))
        counts["unexpected"] += item.get("unexpected", False)
        counts["complete" if item["state"] == "available" else item["state"]] += 1
        if item["backlog"]["state"] in ("waiting", "empty"):
            counts[item["backlog"]["state"]+"_scopes"] += 1
        counts["unsuccessful_collections"] += item["collection"].get("values", {}).get("success") is False
        if item["recovery"]["state"] in RECOVERY:
            counts[item["recovery"]["state"]+"_recovery_scopes"] += 1
    for name in COUNTS[1:]:
        require(counts[name] <= out["counts"][name])
        require(out["omitted_items"] != 0 or counts[name] == out["counts"][name])
    return out


def project_progress(raw, progress, times, current, events, role):
    from post_deployment_builds import require, integer, timestamp
    require(role in ("single-box-v2", "control-plane-v2") and isinstance(raw, dict))
    out = {"waiting_transitions": integer(raw.get("waiting_transitions"), progress["observations"])}
    for name in ("empty_throughout_observed_polls", "observed_incomplete_evidence"):
        require(type(raw.get(name)) is bool)
        out[name] = raw[name]
    clocks = {}
    for name in CLOCKS:
        if name in raw:
            clocks[name], out[name] = timestamp(raw[name]), raw[name]
    if progress["observations"] == 0:
        require(not clocks and not any(out.values()))
        return out
    first, last = times["first_observed_at"], times["last_observed_at"]
    require(all(first <= at <= last for at in clocks.values()))
    for a, b in (("first_waiting_at", "last_waiting_at"), ("first_empty_at", "last_empty_at")):
        require((a in clocks) == (b in clocks))
        if a in clocks:
            require(clocks[a] <= clocks[b])
    if out["waiting_transitions"]:
        require("first_empty_at" in clocks and "last_waiting_at" in clocks and clocks["first_empty_at"] <= clocks["last_waiting_at"])
    health = current.get("handoff_health")
    require(not diagnostics.selected(current,"handoff_health") or health is not None and not health["incomplete"] and health["state"] != "unknown" or out["observed_incomplete_evidence"])
    if health and health["counts"]["waiting_scopes"]:
        require(clocks.get("last_waiting_at") == last)
    if health and health["state"] == "empty":
        require(clocks.get("last_empty_at") == last)
    if out["empty_throughout_observed_polls"]:
        require(not diagnostics.selected(current,"handoff_health") or health is not None and health["state"] == "empty" and not out["observed_incomplete_evidence"] and "first_waiting_at" not in clocks)
        require(clocks.get("first_empty_at") == first and clocks.get("last_empty_at") == last)
    minimum_transitions, last_empty = 0, False
    for event in events:
        if not diagnostics.selected(event,"handoff_health"):
            last_empty = False
            require(not out["empty_throughout_observed_polls"])
            continue
        if "handoff_health" in event.get("omitted_sections", ()):
            continue
        value, at = event.get("handoff_health"), timestamp(event["at"])
        require(value is not None and not value["incomplete"] and value["state"] != "unknown" or out["observed_incomplete_evidence"])
        if value and value["counts"]["waiting_scopes"]:
            require("first_waiting_at" in clocks and clocks["first_waiting_at"] <= at <= clocks["last_waiting_at"])
        if value and value["state"] == "empty":
            require("first_empty_at" in clocks and clocks["first_empty_at"] <= at <= clocks["last_empty_at"])
            last_empty = True
        elif value and value["counts"]["waiting_scopes"]:
            minimum_transitions += last_empty
            last_empty = False
        require(not out["empty_throughout_observed_polls"] or value is not None and value["state"] == "empty")
    require(out["waiting_transitions"] >= minimum_transitions)
    if diagnostics.selected(current,"handoff_health") and events and diagnostics.selected(events[-1],"handoff_health") and "handoff_health" not in events[-1].get("omitted_sections", ()):
        latest = {key: health[key] for key in ("state", "reason", "counts", "incomplete") if health and key in health} if health else None
        require(events[-1].get("handoff_health") == latest)
    return out


def fit(builds, report, maximum):
    h = builds.get("handoff_health")
    while h and h["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode())+1 > maximum:
        h["items"].pop()
        h["omitted_items"] += 1
        builds["handoff_envelope_omitted_items"] = builds.get("handoff_envelope_omitted_items", 0)+1


def summary(builds):
    if not diagnostics.selected(builds,"handoff_health"):
        return ""
    h = builds.get("handoff_health")
    if h is None and builds.get("evidence", {}).get("expected_role") not in ("single-box-v2", "control-plane-v2") and "handoff_health_unavailable_reason" not in builds:
        return ""
    text = "\nBuild-to-image handoffs (advisory): **"+(h["state"] if h else "unknown")+"**.\n\n"
    if h:
        text += ", ".join(name+"="+str(h["counts"][name]) for name in COUNTS)+".\n"
        text += "Pending counts belong to individual recovery scopes and may overlap; no fleet total or deployment completion is inferred.\n"
        text += "Handoff preview omitted_items="+str(h["omitted_items"])+"; envelope_omitted_items="+str(builds.get("handoff_envelope_omitted_items", 0))+".\n"
    else:
        text += "Handoff evidence unavailable ("+builds.get("handoff_health_unavailable_reason", "handoff_evidence_not_returned")+").\n"
    progress = builds.get("progress", {})
    if "handoff_health" in progress:
        p = progress["handoff_health"]
        text += "\nObserved handoff history: waiting_transitions="+str(p["waiting_transitions"])+", empty_throughout_observed_polls="+str(p["empty_throughout_observed_polls"]).lower()+", observed_incomplete_evidence="+str(p["observed_incomplete_evidence"]).lower()+".\n"
        for name in CLOCKS:
            if name in p:
                text += "- handoff_"+name+": "+p[name]+"\n"
    if "handoff_health_unavailable_reason" in progress:
        text += "\nHandoff history unavailable ("+progress["handoff_health_unavailable_reason"]+").\n"
    return text
