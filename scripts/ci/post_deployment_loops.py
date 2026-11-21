"""Closed advisory projection of registered daemon-loop reports."""

import datetime as dt
import re

COUNTS = ("expected", "observed", "no_reported_stall", "stalled", "missing", "stale", "conflicting", "unavailable", "reported_stalls", "unexpected")
STATES = ("no_reported_stall", "stalled", "missing", "stale", "conflicting", "unavailable")
CLOCKS = ("first_stall_at", "last_stall_at", "first_no_reported_stall_at", "last_no_reported_stall_at", "last_recovery_at", "last_regression_at")
REASONS = {
    "missing": ("expected_target_missing", "loop_stall_missing", "loop_age_missing"),
    "stale": ("scrape_stale", "loop_sample_stale"),
    "conflicting": ("conflicting_targets", "conflicting_loop_series"),
    "unavailable": ("source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "loop_sample_before_boundary", "invalid_stall_value", "invalid_loop_age"),
}


def expected_loops(daemon):
    return ("runtime", "main") if daemon == "schedd" else ("runtime", "sweep") if daemon == "vmmd" else ("runtime",)


def project_summary(raw, base):
    from post_deployment_builds import require, integer, NODES_MAX, SAMPLES_MAX
    require(isinstance(raw, dict) and isinstance(raw.get("counts"), dict))
    jobs = base["jobs"]
    maximum = sum((NODES_MAX+SAMPLES_MAX)*len(expected_loops(job["daemon"])) for job in jobs)
    counts = {name: integer(raw["counts"].get(name), maximum) for name in COUNTS}
    total = sum(counts[name] for name in STATES)
    expected = sum(job["expected"]*len(expected_loops(job["daemon"])) for job in jobs)
    per_target = max(len(expected_loops(job["daemon"])) for job in jobs)
    require(counts["expected"] == expected and total >= expected)
    require(base["counts"]["observed"] <= counts["observed"] <= per_target*base["counts"]["observed"])
    require(counts["no_reported_stall"]+counts["stalled"] <= counts["observed"] <= total)
    require(counts["stalled"] <= counts["reported_stalls"] <= counts["observed"] and counts["unexpected"] <= counts["observed"])
    incomplete = any(job["state"] != "available" for job in jobs) or any(counts[name] for name in ("missing", "stale", "conflicting", "unavailable", "unexpected"))
    require(type(raw.get("incomplete")) is bool and raw["incomplete"] == bool(incomplete))
    if counts["reported_stalls"]:
        state, reason = "stalled", "reported_loop_stall"
    elif incomplete:
        state, reason = "incomplete", "incomplete_loop_evidence"
    elif counts["no_reported_stall"]:
        state, reason = "no_reported_stall", ""
    else:
        state, reason = "unknown", "loop_evidence_unavailable"
    require(raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"state": state, "counts": counts, "incomplete": bool(incomplete)}
    if "reason" in raw:
        out["reason"] = reason
    return out


def project_item(raw, base):
    from post_deployment_builds import require, timestamp, MAX_AGE_SECONDS, CLOCK_SKEW_SECONDS, NODE_MAX_BYTES
    from post_deployment_process import number
    require(isinstance(raw, dict))
    job, daemon, loop = raw.get("job"), raw.get("daemon"), raw.get("loop")
    require(any(item["job"] == job and item["daemon"] == daemon for item in base["jobs"]) and loop in expected_loops(daemon))
    state, reason = raw.get("state"), raw.get("reason", "")
    out = {"job": job, "daemon": daemon, "loop": loop, "state": state}
    if "reason" in raw:
        out["reason"] = reason
    for field, pattern, maximum in (("node", r"[A-Za-z0-9][A-Za-z0-9_.-]*", NODE_MAX_BYTES), ("target_id", r"[0-9a-f]{64}", 64)):
        if field in raw:
            value = raw[field]
            require(isinstance(value, str) and len(value.encode()) <= maximum and (not value or re.fullmatch(pattern, value)))
            out[field] = value
    if "unexpected" in raw:
        require(type(raw["unexpected"]) is bool and (not raw["unexpected"] or bool(out.get("target_id"))))
        out["unexpected"] = raw["unexpected"]
    generated, after = timestamp(base["generated_at"]), timestamp(base["evidence_after"])
    skew = dt.timedelta(seconds=CLOCK_SKEW_SECONDS)
    clocks = {}
    for field in ("scrape_observed_at", "stall_observed_at", "age_observed_at"):
        if field in raw:
            clocks[field] = timestamp(raw[field])
            require(clocks[field] <= generated+skew)
            out[field] = raw[field]
    def fresh(field):
        return field in clocks and generated-dt.timedelta(seconds=MAX_AGE_SECONDS) <= clocks[field] and clocks[field] >= after-skew
    up = bool(out.get("target_id")) and fresh("scrape_observed_at")
    if "reported_stalled" in raw:
        require(type(raw["reported_stalled"]) is bool and up and fresh("stall_observed_at"))
        require(reason not in ("expected_target_missing", "loop_stall_missing", "invalid_stall_value", "scrape_stale", "scrape_before_boundary", "scrape_down", "conflicting_targets"))
        out["reported_stalled"] = raw["reported_stalled"]
    if "age_seconds" in raw:
        require(up and fresh("age_observed_at"))
        require(reason not in ("expected_target_missing", "loop_age_missing", "invalid_loop_age", "scrape_stale", "scrape_before_boundary", "scrape_down", "conflicting_targets"))
        out["age_seconds"] = number(raw["age_seconds"])
    if state in ("no_reported_stall", "stalled"):
        require(reason == "" and "reported_stalled" in out and "age_seconds" in out)
        require((state == "stalled") == out["reported_stalled"])
    else:
        require(isinstance(state, str) and state in REASONS and reason in REASONS[state])
    return out


def project(raw, base):
    from post_deployment_builds import require, integer, PREVIEW_MAX, NODES_MAX, SAMPLES_MAX
    out = project_summary(raw, base)
    require(isinstance(raw.get("items"), list) and len(raw["items"]) <= PREVIEW_MAX)
    items = [project_item(item, base) for item in raw["items"]]
    maximum = sum((NODES_MAX+SAMPLES_MAX)*len(expected_loops(job["daemon"])) for job in base["jobs"])
    omitted = integer(raw.get("omitted_items"), maximum)
    require(sum(out["counts"][name] for name in STATES) == len(items)+omitted)
    keys = [(item["job"], item["loop"], item.get("target_id", ""), item.get("node", "")) for item in items]
    require(len(set(keys)) == len(keys))
    for name in STATES:
        require(sum(item["state"] == name for item in items) <= out["counts"][name])
    require(sum(bool(item.get("target_id")) for item in items) <= out["counts"]["observed"])
    require(sum(item.get("reported_stalled", False) for item in items) <= out["counts"]["reported_stalls"])
    require(sum(item.get("unexpected", False) for item in items) <= out["counts"]["unexpected"])
    out.update(items=items, omitted_items=omitted)
    return out


def project_progress(raw, progress, times, current, events):
    from post_deployment_builds import require, integer, timestamp
    require(isinstance(raw, dict))
    out = {"stall_regressions": integer(raw.get("stall_regressions"), progress["observations"])}
    for name in ("no_reported_stall_throughout_observed_polls", "observed_incomplete_evidence"):
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
    for a, b in (("first_stall_at", "last_stall_at"), ("first_no_reported_stall_at", "last_no_reported_stall_at")):
        require((a in clocks) == (b in clocks))
        if a in clocks:
            require(clocks[a] <= clocks[b])
    require(bool(out["stall_regressions"]) == ("last_regression_at" in clocks))
    if out["stall_regressions"]:
        require("first_no_reported_stall_at" in clocks and "last_stall_at" in clocks)
        require(clocks["first_no_reported_stall_at"] <= clocks["last_regression_at"] <= clocks["last_stall_at"])
    if "last_recovery_at" in clocks:
        require("first_no_reported_stall_at" in clocks and "first_stall_at" in clocks)
        require(max(clocks["first_no_reported_stall_at"], clocks["first_stall_at"]) <= clocks["last_recovery_at"] <= clocks["last_no_reported_stall_at"])
    loops = current.get("loop_health")
    if loops is None or loops["incomplete"]:
        require(out["observed_incomplete_evidence"])
    if loops and loops["state"] == "no_reported_stall":
        require(clocks.get("last_no_reported_stall_at") == last)
    if loops and loops["counts"]["reported_stalls"]:
        require(clocks.get("last_stall_at") == last)
    if out["no_reported_stall_throughout_observed_polls"]:
        require(loops is not None and loops["state"] == "no_reported_stall" and not out["observed_incomplete_evidence"] and "first_stall_at" not in clocks)
        require(clocks.get("first_no_reported_stall_at") == first and clocks.get("last_no_reported_stall_at") == last)
    for event in events:
        if "loop_health" in event.get("omitted_sections", ()):
            continue
        value, at = event.get("loop_health"), timestamp(event["at"])
        require(value is not None and not value["incomplete"] or out["observed_incomplete_evidence"])
        if value and value["counts"]["reported_stalls"]:
            require("first_stall_at" in clocks and clocks["first_stall_at"] <= at <= clocks["last_stall_at"])
        if value and value["state"] == "no_reported_stall":
            require("first_no_reported_stall_at" in clocks and clocks["first_no_reported_stall_at"] <= at <= clocks["last_no_reported_stall_at"])
        require(not out["no_reported_stall_throughout_observed_polls"] or value is not None and value["state"] == "no_reported_stall")
    if events and "loop_health" not in events[-1].get("omitted_sections", ()):
        latest = {key: loops[key] for key in ("state", "counts", "incomplete", "reason") if loops and key in loops} if loops else None
        require(events[-1].get("loop_health") == latest)
    return out


def fit(builds, report, maximum):
    import json
    loops = builds.get("loop_health")
    while loops and loops["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode())+1 > maximum:
        loops["items"].pop()
        loops["omitted_items"] += 1
        builds["loop_envelope_omitted_items"] = builds.get("loop_envelope_omitted_items", 0)+1


def summary(builds):
    loops = builds.get("loop_health")
    text = "\nReported work-loop health (advisory): **"+(loops["state"] if loops else "unknown")+"**.\n\n"
    if loops:
        text += ", ".join(name+"="+str(loops["counts"][name]) for name in COUNTS)+".\n"
        text += "Loop preview omitted_items="+str(loops["omitted_items"])+"; envelope_omitted_items="+str(builds.get("loop_envelope_omitted_items", 0))+".\n"
    else:
        text += "Loop evidence unavailable ("+builds.get("loop_health_unavailable_reason", "loop_evidence_not_returned")+").\n"
    progress = builds.get("progress", {})
    if "loop_health" in progress:
        p = progress["loop_health"]
        text += "\nObserved loop history: stall_regressions="+str(p["stall_regressions"])+", no_reported_stall_throughout_observed_polls="+str(p["no_reported_stall_throughout_observed_polls"]).lower()+", observed_incomplete_evidence="+str(p["observed_incomplete_evidence"]).lower()+".\n"
        for name in CLOCKS:
            if name in p:
                text += "- loop_"+name+": "+p[name]+"\n"
    if "loop_health_unavailable_reason" in progress:
        text += "\nLoop history unavailable ("+progress["loop_health_unavailable_reason"]+").\n"
    return text
