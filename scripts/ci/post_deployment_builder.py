"""Closed advisory projection of process-local builder progress and pressure."""

import datetime as dt
import post_deployment_diagnostics as diagnostics
import json
import re

COUNTS = ("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected",
          "overdue_workers", "waiting_workers", "working_workers", "draining_workers", "waiting_queues", "saturated_slots")
QUALITY = ("complete", "missing", "stale", "conflicting", "unavailable")
WORKERS = ("waiting", "working", "draining", "overdue")
CLOCKS = ("first_overdue_at", "last_overdue_at", "first_within_allowance_at", "last_within_allowance_at", "last_recovery_at", "last_regression_at")
REASONS = {
    "missing": ("expected_target_missing", "metric_missing"),
    "stale": ("scrape_stale", "metric_stale"),
    "conflicting": ("conflicting_targets", "conflicting_series"),
    "unavailable": ("source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "metric_before_boundary", "invalid_value"),
}


def project_summary(raw, base):
    from post_deployment_builds import require, integer, SAMPLES_MAX, NODES_MAX
    require(isinstance(raw, dict) and isinstance(raw.get("counts"), dict))
    require(base["expected_role"] in ("single-box-v2", "control-plane-v2"))
    jobs = [job for job in base["jobs"] if job["job"] == "builderd" and job["daemon"] == "builderd"]
    require(len(jobs) == 1)
    c = {name: integer(raw["counts"].get(name), 2*(SAMPLES_MAX+NODES_MAX)) for name in COUNTS}
    total = sum(c[name] for name in QUALITY)
    workers = sum(c[name+"_workers"] for name in WORKERS)
    require(c["expected"] == jobs[0]["expected"] and c["expected"] <= total <= SAMPLES_MAX+NODES_MAX)
    require(c["observed"] <= min(base["counts"]["observed"], total))
    require(c["complete"] <= workers <= c["observed"] and c["unexpected"] <= c["observed"])
    require(c["saturated_slots"] <= c["observed"] and c["waiting_queues"] <= 2*c["observed"])
    incomplete = jobs[0]["state"] != "available" or any(c[name] for name in ("missing", "stale", "conflicting", "unavailable", "unexpected"))
    state, reason = ("overdue", "worker_progress_overdue") if c["overdue_workers"] else ("incomplete", "incomplete_builder_evidence") if incomplete else ("observed", "") if c["complete"] else ("unknown", "builder_evidence_unavailable")
    require(type(raw.get("incomplete")) is bool and raw["incomplete"] == incomplete)
    require(raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"state": state, "counts": c, "incomplete": incomplete}
    if "reason" in raw:
        out["reason"] = reason
    return out


def worker_values(raw, base, state):
    from post_deployment_builds import require, timestamp, CLOCK_SKEW_SECONDS
    from post_deployment_process import number
    require(isinstance(raw, dict))
    out = {}
    for name in ("enabled", "inflight"):
        require(type(raw.get(name)) is bool)
        out[name] = raw[name]
    require(out["enabled"] or not out["inflight"])
    for name in ("interval_seconds", "busy_allowance_seconds", "progress_age_seconds", "progress_allowance_seconds"):
        out[name] = number(raw.get(name))
    require(out["interval_seconds"] > 0 and out["busy_allowance_seconds"] > 0)
    generated = timestamp(base["generated_at"])
    clocks = {}
    for name in ("started_at", "last_attempt_at", "last_completed_at"):
        if name == "started_at" or name in raw:
            clocks[name] = timestamp(raw.get(name))
            require(generated-dt.timedelta(seconds=315360000) <= clocks[name] <= generated)
            out[name] = raw[name]
    started, attempt, completed = clocks["started_at"], clocks.get("last_attempt_at"), clocks.get("last_completed_at")
    require(all(at >= started for at in clocks.values()))
    require(completed is None or attempt is not None)
    require(not out["inflight"] or attempt is not None)
    if attempt is not None and completed is not None:
        require(attempt >= completed if out["inflight"] else completed >= attempt)
    origin = attempt if out["inflight"] else completed or started
    age = (generated-origin).total_seconds()
    allowance = (out["busy_allowance_seconds"] if out["inflight"] else 0)+2*out["interval_seconds"]+2*CLOCK_SKEW_SECONDS
    require(abs(out["progress_age_seconds"]-age) <= 0.000001 and out["progress_allowance_seconds"] == allowance)
    expected = "draining" if not out["enabled"] else "overdue" if out["progress_age_seconds"] > allowance else "working" if out["inflight"] else "waiting"
    require(state == expected)
    return out


def slot_values(raw):
    from post_deployment_builds import require, integer, SAMPLES_MAX
    require(isinstance(raw, dict))
    out = {name: integer(raw.get(name), SAMPLES_MAX) for name in ("used", "limit")}
    require(0 <= out["used"] <= out["limit"] and out["limit"] > 0)
    return out


def queue_values(raw, base):
    from post_deployment_builds import require, integer, SAMPLES_MAX, timestamp
    from post_deployment_process import number
    require(isinstance(raw, dict) and type(raw.get("enabled")) is bool)
    out = {"enabled": raw["enabled"], **{name: integer(raw.get(name), SAMPLES_MAX) for name in ("waiting", "active", "capacity", "workers")}}
    out["oldest_wait_seconds"] = number(raw.get("oldest_wait_seconds"))
    require(out["workers"] > 0 and out["active"] <= out["workers"])
    require(out["waiting"]+out["active"] <= out["capacity"]+out["workers"])
    if out["waiting"] == 0:
        require("oldest_enqueued_at" not in raw and out["oldest_wait_seconds"] == 0)
    else:
        oldest, generated = timestamp(raw.get("oldest_enqueued_at")), timestamp(base["generated_at"])
        age = (generated-oldest).total_seconds()
        require(0 <= age <= 315360000 and abs(age-out["oldest_wait_seconds"]) <= 0.000001)
        out["oldest_enqueued_at"] = raw["oldest_enqueued_at"]
    return out


def project_item(raw, base):
    from post_deployment_builds import require, timestamp, MAX_AGE_SECONDS, CLOCK_SKEW_SECONDS, NODE_MAX_BYTES
    require(isinstance(raw, dict) and isinstance(raw.get("queues"), list) and len(raw["queues"]) == 2)
    generated, after = timestamp(base["generated_at"]), timestamp(base["evidence_after"])
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
    if "scrape_observed_at" in raw:
        scrape = timestamp(raw["scrape_observed_at"])
        require(scrape <= generated+dt.timedelta(seconds=CLOCK_SKEW_SECONDS))
        out["scrape_observed_at"] = raw["scrape_observed_at"]

    def fresh(at):
        return at is not None and max(generated-dt.timedelta(seconds=MAX_AGE_SECONDS), after-dt.timedelta(seconds=CLOCK_SKEW_SECONDS)) <= at <= generated+dt.timedelta(seconds=CLOCK_SKEW_SECONDS)

    def component(value, projector, worker=False):
        require(isinstance(value, dict))
        state, reason = value.get("state"), value.get("reason", "")
        result = {"state": state}
        if "reason" in value:
            result["reason"] = reason
        if (worker and state in WORKERS) or (not worker and state == "available"):
            require(reason == "" and out.get("target_id") and fresh(scrape) and fresh(timestamp(value.get("observed_at"))))
            result["observed_at"] = value["observed_at"]
            result["values"] = projector(value.get("values"))
        else:
            require(isinstance(state, str) and state in REASONS and reason in REASONS[state])
            require(value.get("values") is None and "observed_at" not in value)
        return result

    out["worker"] = component(raw.get("worker"), lambda v: worker_values(v, base, raw["worker"]["state"]), True)
    out["slots"] = component(raw.get("slots"), slot_values)
    out["queues"] = []
    for i, name in enumerate(("build", "cancel")):
        queue = raw["queues"][i]
        require(isinstance(queue, dict) and queue.get("queue") == name)
        out["queues"].append({"queue": name, **component(queue, lambda v: queue_values(v, base))})
    expected = next((value["state"] for value in (out["worker"], out["slots"], *out["queues"]) if value["state"] in REASONS), "available")
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
        if item["worker"]["state"] in WORKERS:
            counts[item["worker"]["state"]+"_workers"] += 1
        slots = item["slots"].get("values")
        counts["saturated_slots"] += bool(slots and slots["used"] == slots["limit"])
        counts["waiting_queues"] += sum(bool(q.get("values", {}).get("waiting")) for q in item["queues"])
    for name in COUNTS[1:]:
        require(counts[name] <= out["counts"][name])
        require(out["omitted_items"] != 0 or counts[name] == out["counts"][name])
    return out


def within(summary):
    return summary is not None and not summary["incomplete"] and summary["counts"]["complete"] > 0 and not summary["counts"]["overdue_workers"] and not summary["counts"]["draining_workers"]


def project_progress(raw, progress, times, current, events, role):
    from post_deployment_builds import require, integer, timestamp
    require(role in ("single-box-v2", "control-plane-v2") and isinstance(raw, dict))
    out = {"overdue_regressions": integer(raw.get("overdue_regressions"), progress["observations"])}
    for name in ("within_allowance_throughout_observed_polls", "observed_incomplete_evidence"):
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
    for a, b in (("first_overdue_at", "last_overdue_at"), ("first_within_allowance_at", "last_within_allowance_at")):
        require((a in clocks) == (b in clocks))
        if a in clocks:
            require(clocks[a] <= clocks[b])
    require(bool(out["overdue_regressions"]) == ("last_regression_at" in clocks))
    if out["overdue_regressions"]:
        require("first_within_allowance_at" in clocks and "last_overdue_at" in clocks)
        require(clocks["first_within_allowance_at"] <= clocks["last_regression_at"] <= clocks["last_overdue_at"])
    if "last_recovery_at" in clocks:
        require("first_overdue_at" in clocks and "first_within_allowance_at" in clocks)
        require(max(clocks["first_overdue_at"], clocks["first_within_allowance_at"]) <= clocks["last_recovery_at"] <= clocks["last_within_allowance_at"])
    health = current.get("builder_health")
    require(not diagnostics.selected(current,"builder_health") or health is not None and not health["incomplete"] and health["state"] != "unknown" or out["observed_incomplete_evidence"])
    if within(health):
        require(clocks.get("last_within_allowance_at") == last)
    if health and health["counts"]["overdue_workers"]:
        require(clocks.get("last_overdue_at") == last)
    if out["within_allowance_throughout_observed_polls"]:
        require(within(health) and not out["observed_incomplete_evidence"] and "first_overdue_at" not in clocks)
        require(clocks.get("first_within_allowance_at") == first and clocks.get("last_within_allowance_at") == last)
    for event in events:
        if not diagnostics.selected(event,"builder_health"):
            require(not out["within_allowance_throughout_observed_polls"])
            continue
        if "builder_health" in event.get("omitted_sections", ()):
            continue
        value, at = event.get("builder_health"), timestamp(event["at"])
        require(value is not None and not value["incomplete"] and value["state"] != "unknown" or out["observed_incomplete_evidence"])
        if value and value["counts"]["overdue_workers"]:
            require("first_overdue_at" in clocks and clocks["first_overdue_at"] <= at <= clocks["last_overdue_at"])
        if within(value):
            require("first_within_allowance_at" in clocks and clocks["first_within_allowance_at"] <= at <= clocks["last_within_allowance_at"])
        require(not out["within_allowance_throughout_observed_polls"] or within(value))
    if diagnostics.selected(current,"builder_health") and events and diagnostics.selected(events[-1],"builder_health") and "builder_health" not in events[-1].get("omitted_sections", ()):
        latest = {key: health[key] for key in ("state", "reason", "counts", "incomplete") if health and key in health} if health else None
        require(events[-1].get("builder_health") == latest)
    return out


def fit(builds, report, maximum):
    health = builds.get("builder_health")
    while health and health["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode())+1 > maximum:
        health["items"].pop()
        health["omitted_items"] += 1
        builds["builder_envelope_omitted_items"] = builds.get("builder_envelope_omitted_items", 0)+1


def summary(builds):
    if not diagnostics.selected(builds,"builder_health"):
        return ""
    health = builds.get("builder_health")
    if health is None and builds.get("evidence", {}).get("expected_role") not in ("single-box-v2", "control-plane-v2") and "builder_health_unavailable_reason" not in builds:
        return ""
    text = "\nBuilder progress and pressure (advisory): **"+(health["state"] if health else "unknown")+"**.\n\n"
    if health:
        text += ", ".join(name+"="+str(health["counts"][name]) for name in COUNTS)+".\n"
        text += "Builder preview omitted_items="+str(health["omitted_items"])+"; envelope_omitted_items="+str(builds.get("builder_envelope_omitted_items", 0))+".\n"
    else:
        text += "Builder evidence unavailable ("+builds.get("builder_health_unavailable_reason", "builder_evidence_not_returned")+").\n"
    progress = builds.get("progress", {})
    if "builder_health" in progress:
        p = progress["builder_health"]
        text += "\nObserved builder history: overdue_regressions="+str(p["overdue_regressions"])+", within_allowance_throughout_observed_polls="+str(p["within_allowance_throughout_observed_polls"]).lower()+", observed_incomplete_evidence="+str(p["observed_incomplete_evidence"]).lower()+".\n"
        for name in CLOCKS:
            if name in p:
                text += "- builder_"+name+": "+p[name]+"\n"
    if "builder_health_unavailable_reason" in progress:
        text += "\nBuilder history unavailable ("+progress["builder_health_unavailable_reason"]+").\n"
    return text
