"""Closed projection of sampled process-local operation counter changes."""
import post_deployment_diagnostics as diagnostics
import json
import datetime as dt
import re
import post_deployment_process as process
from post_deployment_handoff import PENDING_MAX

OPS = ("restore", "renew", "execute", "destroy", "complete")
OUTCOMES = ("success", "error", "canceled", "lease_lost", "empty", "cancellation_pending", "capacity")
COUNTS = ("expected", "observed", "increased", "unchanged", "baseline", "missing", "stale", "conflicting", "unavailable", "unexpected")
FIELDS = ("increased", "unchanged", "baseline", "unknown", "error_increases", "cancellations", "lease_losses", "capacity_refusals")
BOOLS = ("observed_increases", "observed_errors", "observed_cancellations", "observed_lease_losses", "observed_capacity_refusals", "observed_baseline", "observed_incomplete_evidence")
SCOPE = "process_local_counter_observations"
BAD = {"missing": ("expected_target_missing", "metric_missing"), "stale": ("scrape_stale", "metric_stale"),
       "conflicting": ("conflicting_targets", "conflicting_series", "conflicting_sample_times"),
       "unavailable": ("source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "metric_before_boundary", "invalid_value")}


def project_summary(raw, base):
    from post_deployment_builds import require, integer, SAMPLES_MAX, NODES_MAX
    from post_deployment_app_task import jobs_for_role
    names = jobs_for_role(base["expected_role"])
    require(names and isinstance(raw, dict) and raw.get("scope") == SCOPE and isinstance(raw.get("counts"), dict))
    jobs = [j for j in base["jobs"] if j["job"] in names and j["daemon"] == "schedd"]
    require(len(jobs) == len(names))
    maximum = len(names)*(SAMPLES_MAX+NODES_MAX)*35
    c = {k: integer(raw["counts"].get(k), maximum) for k in COUNTS}
    unknown = sum(c[k] for k in BAD)
    total = c["increased"]+c["unchanged"]+c["baseline"]+unknown
    require(c["expected"] == 35*sum(j["expected"] for j in jobs) and c["expected"] <= total <= maximum and total % 35 == 0)
    require(c["observed"] <= min(total,base["counts"]["observed"]*35) and c["increased"]+c["unchanged"]+c["baseline"] <= c["observed"] and c["unexpected"] <= total and c["unexpected"] % 35 == 0)
    require(isinstance(raw.get("operations"), list) and len(raw["operations"]) == len(OPS))
    operations = []
    for name, value in zip(OPS, raw["operations"]):
        require(isinstance(value, dict) and value.get("operation") == name)
        o = {k: integer(value.get(k), maximum) for k in FIELDS}
        require(sum(o[k] for k in ("increased", "unchanged", "baseline", "unknown")) == total//5)
        require(sum(o[k] for k in FIELDS[4:]) <= o["increased"] and max(o[k] for k in FIELDS[4:]) <= total//35)
        operations.append({"operation": name, **o})
    for k, expected in (("increased", c["increased"]), ("unchanged", c["unchanged"]), ("baseline", c["baseline"]), ("unknown", unknown)):
        require(sum(o[k] for o in operations) == expected)
    incomplete = any(j["state"] != "available" for j in jobs) or unknown > 0 or c["unexpected"] > 0
    state, reason = ("increased", "operation_increases_observed") if c["increased"] else ("baseline", "comparison_not_requested") if c["baseline"] else ("incomplete", "incomplete_operation_evidence") if incomplete else ("unchanged", "") if c["unchanged"] else ("unknown", "operation_evidence_unavailable")
    require(type(raw.get("incomplete")) is bool and raw["incomplete"] == incomplete and raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"scope": SCOPE, "state": state, "counts": c, "operations": operations, "incomplete": incomplete}
    if "reason" in raw:
        out["reason"] = reason
    return out


def comparison(current, previous, p):
    if p is None or p["state"] != "fresh":
        return "unavailable", "process_evidence_unavailable"
    if p["comparison"] != "unchanged":
        return "unavailable", {"changed": "process_changed", "new_target": "new_target", "repeated_sample": "repeated_process_sample"}.get(p["comparison"], "process_comparison_unavailable")
    if process.sample_ns(current["observed_at"]) != process.sample_ns(p["observed_at"]) or process.sample_ns(previous["observed_at"]) != process.sample_ns(p["previous_observed_at"]) or process.sample_ns(current["observed_at"]) <= process.sample_ns(previous["observed_at"]):
        return "unavailable", "sample_clock_mismatch"
    if abs((process.sample_ns(current["observed_at"])-process.sample_ns(previous["observed_at"]))/1000000000 - (p["uptime_seconds"]-p["previous_uptime_seconds"])) > process.RESET_TOLERANCE_SECONDS:
        return "unavailable", "process_window_uncertain"
    if current["value"] < previous["value"]:
        return "unavailable", "counter_reset"
    return ("increased", "") if current["value"] > previous["value"] else ("unchanged", "")


def project_item(raw, base):
    return project_counter_item(raw, base, [{"operation": op, "outcome": outcome} for op in OPS for outcome in OUTCOMES])


def project_counter_item(raw, base, dimensions):
    from post_deployment_builds import require, integer, timestamp, NODE_MAX_BYTES, CLOCK_SKEW_SECONDS
    from post_deployment_app_task import jobs_for_role
    require(isinstance(raw, dict) and raw.get("job") in jobs_for_role(base["expected_role"]))
    now, after = timestamp(base["generated_at"]), timestamp(base["evidence_after"])
    comparison_at = base["process"].get("comparison_at")
    prior = timestamp(comparison_at) if comparison_at else None
    out = {"job": raw["job"]}
    for k, pattern, maximum in (("node", r"[A-Za-z0-9][A-Za-z0-9_.-]*", NODE_MAX_BYTES), ("target_id", r"[0-9a-f]{64}", 64)):
        if k in raw:
            v = raw[k]
            require(isinstance(v, str) and len(v.encode()) <= maximum and (not v or re.fullmatch(pattern, v)))
            out[k] = v
    if "unexpected" in raw:
        require(type(raw["unexpected"]) is bool and (not raw["unexpected"] or out.get("target_id")))
        out["unexpected"] = raw["unexpected"]
    if raw.get("scrape_observed_at") is not None:
        require(timestamp(raw["scrape_observed_at"]) <= now+dt.timedelta(seconds=CLOCK_SKEW_SECONDS))
        out["scrape_observed_at"] = raw["scrape_observed_at"]
    out["process"] = process.project_item(raw.get("process"), out, now, after, prior)
    require(isinstance(raw.get("signals"), list) and len(raw["signals"]) == len(dimensions))

    def point(value, at):
        require(isinstance(value, dict))
        return {"value": integer(value.get("value"), PENDING_MAX), "observed_at": process.fresh_time(value.get("observed_at"), at, after)}

    signals = []
    for dimension, value in zip(dimensions, raw["signals"]):
        require(isinstance(value, dict) and all(value.get(k) == v for k, v in dimension.items()))
        state, reason = value.get("state"), value.get("reason", "")
        s = {**dimension, "state": state}
        if "reason" in value:
            s["reason"] = reason
        if value.get("current") is None:
            require(isinstance(state, str) and state in BAD and reason in BAD[state] and value.get("previous") is None and value.get("delta") is None)
        else:
            require(out.get("target_id"))
            process.fresh_time(out.get("scrape_observed_at"), now, after)
            s["current"] = point(value["current"], now)
            if prior is None:
                require(state == "baseline" and reason == "" and value.get("previous") is None and value.get("delta") is None)
            elif value.get("previous") is None:
                require(isinstance(state, str) and state in BAD and reason in tuple("previous_"+r for r in BAD[state]) and value.get("delta") is None)
            else:
                s["previous"] = point(value["previous"], prior)
                require((state, reason) == comparison(s["current"], s["previous"], out["process"]))
                if state in ("increased", "unchanged"):
                    delta = integer(value.get("delta"), PENDING_MAX)
                    require(delta == s["current"]["value"]-s["previous"]["value"])
                    s["delta"] = delta
                else:
                    require(value.get("delta") is None)
        signals.append(s)
    out["signals"] = signals
    return out


def account(items):
    c = dict.fromkeys(COUNTS, 0)
    operations = [{"operation": op, **dict.fromkeys(FIELDS, 0)} for op in OPS]
    for item in items:
        for s in item["signals"]:
            o = operations[OPS.index(s["operation"])]
            c["observed"] += "current" in s
            c["unexpected"] += item.get("unexpected", False)
            state = s["state"]
            c[state] += 1
            o[state if state in ("increased", "unchanged", "baseline") else "unknown"] += 1
            if state == "increased":
                k = {"error": "error_increases", "canceled": "cancellations", "lease_lost": "lease_losses", "capacity": "capacity_refusals"}.get(s["outcome"])
                if k:
                    o[k] += 1
    return c, operations


def project(raw, base):
    from post_deployment_builds import require, integer, PREVIEW_MAX, SAMPLES_MAX, NODES_MAX
    require(isinstance(base.get("process"), dict))
    out = project_summary(raw, base)
    require(raw.get("comparison_at") == base["process"].get("comparison_at"))
    if raw.get("comparison_at") is not None:
        out["comparison_at"] = raw["comparison_at"]
        require(out["counts"]["baseline"] == 0)
    else:
        require(out["counts"]["increased"]+out["counts"]["unchanged"] == 0)
    require(isinstance(raw.get("items"), list) and len(raw["items"]) <= PREVIEW_MAX)
    out["items"] = [project_item(item, base) for item in raw["items"]]
    out["omitted_items"] = integer(raw.get("omitted_items"), 2*(SAMPLES_MAX+NODES_MAX))
    c = out["counts"]
    require((len(out["items"])+out["omitted_items"])*35 == sum(c[k] for k in ("increased", "unchanged", "baseline", *BAD)))
    keys = [i.get("target_id") or i["job"]+"/missing/"+i.get("node", "") for i in out["items"]]
    require(len(keys) == len(set(keys)))
    part, operations = account(out["items"])
    for k in COUNTS[1:]:
        require(part[k] <= c[k] and (out["omitted_items"] or part[k] == c[k]))
    for p, f in zip(operations, out["operations"]):
        for k in FIELDS:
            require(p[k] <= f[k] and (out["omitted_items"] or p[k] == f[k]))
    return out


def project_progress(raw, progress, times, current, events, role):
    from post_deployment_builds import require, timestamp
    require(role in ("single-box-v2", "control-plane-v2") and isinstance(raw, dict))
    out = {}
    for k in BOOLS:
        require(type(raw.get(k)) is bool)
        out[k] = raw[k]
    clocks = {k: timestamp(raw[k]) for k in ("first_increase_at", "last_increase_at") if k in raw}
    require(bool(clocks) == out["observed_increases"] and len(clocks) in (0, 2))
    if clocks:
        require(times["first_observed_at"] <= clocks["first_increase_at"] <= clocks["last_increase_at"] <= times["last_observed_at"])
        out.update({k: raw[k] for k in clocks})
    require(not any(out[k] for k in BOOLS[1:5]) or out["observed_increases"])
    for value in (current, *events):
        if not diagnostics.selected(value,"app_task_operations"):
            continue
        if value is not current and "app_task_operations" in value.get("omitted_sections", ()):
            continue
        h = value.get("app_task_operations")
        require(h is not None and not h["incomplete"] and h["state"] != "unknown" or out["observed_incomplete_evidence"])
        if not h:
            continue
        require(not h["counts"]["baseline"] or out["observed_baseline"])
        if h["counts"]["increased"]:
            at = timestamp(value["at"]) if "at" in value else times["last_observed_at"]
            require(clocks and clocks["first_increase_at"] <= at <= clocks["last_increase_at"])
        for field, flag in (("error_increases", "observed_errors"), ("cancellations", "observed_cancellations"), ("lease_losses", "observed_lease_losses"), ("capacity_refusals", "observed_capacity_refusals")):
            require(not any(o[field] for o in h["operations"]) or out[flag])
    if progress["observations"] == 0:
        require(not any(out.values()))
    if diagnostics.selected(current,"app_task_operations") and events and diagnostics.selected(events[-1],"app_task_operations") and "app_task_operations" not in events[-1].get("omitted_sections", ()):
        h = current.get("app_task_operations")
        latest = {k: h[k] for k in ("scope", "state", "reason", "counts", "operations", "incomplete") if h and k in h} if h else None
        require(events[-1].get("app_task_operations") == latest)
    return out


def fit(builds, report, maximum):
    h = builds.get("app_task_operations")
    while h and h["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode())+1 > maximum:
        h["items"].pop()
        h["omitted_items"] += 1
        builds["app_task_operations_envelope_omitted_items"] = builds.get("app_task_operations_envelope_omitted_items", 0)+1


def summary(builds):
    if not diagnostics.selected(builds,"app_task_operations"):
        return ""
    h = builds.get("app_task_operations")
    if h is None and builds.get("evidence", {}).get("expected_role") not in ("single-box-v2", "control-plane-v2") and "app_task_operations_unavailable_reason" not in builds:
        return ""
    text = "\nApp-task operation outcomes (advisory): **"+(h["state"] if h else "unknown")+"**.\n\n"
    if h:
        text += ", ".join(k+"="+str(h["counts"][k]) for k in COUNTS)+".\n"
        for o in h["operations"]:
            text += "- "+o["operation"]+": "+", ".join(k+"="+str(o[k]) for k in FIELDS)+"\n"
        text += "Counts describe counter signals, not returned calls or completed tasks. Baselines, restarts, resets, sparse capacity series and source gaps establish no delta. Execute success is not a customer verdict. No rates or new health thresholds are inferred.\n"
    else:
        text += "Operation evidence unavailable ("+builds.get("app_task_operations_unavailable_reason", "operation_evidence_not_returned")+").\n"
    p = builds.get("progress", {}).get("app_task_operations")
    if p:
        text += "\nObserved operation history: "+", ".join(k+"="+str(p[k]).lower() for k in BOOLS)+".\n"
        for k in ("first_increase_at", "last_increase_at"):
            if k in p:
                text += "- "+k+": "+p[k]+"\n"
    return text
