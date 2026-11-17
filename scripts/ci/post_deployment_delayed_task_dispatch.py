"""Closed projection of settled Delayed-task dispatch attempt outcomes."""
import post_deployment_diagnostics as diagnostics
import json

import post_deployment_app_task_operations as counters

COUNTS = counters.COUNTS
FIELDS = ("increased", "unchanged", "baseline", "unknown")
NAMES = ("success", "retry", "failed", "dead_letter")
DIMENSIONS = tuple({"outcome": outcome} for outcome in NAMES)
BOOLS = ("observed_increases", "observed_baseline", "observed_incomplete_evidence")


def dimension_valid(raw, dimension):
 return isinstance(raw, dict) and all(raw.get(k) == v for k,v in dimension.items())


def project_summary(raw, base):
    from post_deployment_builds import require, integer, SAMPLES_MAX, NODES_MAX
    from post_deployment_app_task import jobs_for_role
    names = jobs_for_role(base["expected_role"])
    require(names and isinstance(raw, dict) and raw.get("scope") == counters.SCOPE and isinstance(raw.get("counts"), dict))
    jobs = [j for j in base["jobs"] if j["job"] in names and j["daemon"] == "schedd"]
    require(len(jobs) == len(names))
    maximum = len(names)*(SAMPLES_MAX+NODES_MAX)*len(DIMENSIONS)
    c = {k: integer(raw["counts"].get(k), maximum) for k in COUNTS}
    unknown = sum(c[k] for k in counters.BAD)
    total = sum(c[k] for k in ("increased", "unchanged", "baseline"))+unknown
    require(c["expected"] == len(DIMENSIONS)*sum(j["expected"] for j in jobs) and c["expected"] <= total <= maximum and total % len(DIMENSIONS) == 0)
    require(c["observed"] <= min(total, base["counts"]["observed"]*len(DIMENSIONS)) and sum(c[k] for k in ("increased", "unchanged", "baseline")) <= c["observed"] and c["unexpected"] <= total and c["unexpected"] % len(DIMENSIONS) == 0)
    require(isinstance(raw.get("outcomes"), list) and len(raw["outcomes"]) == len(DIMENSIONS))
    outcomes = []
    for d, value in zip(DIMENSIONS, raw["outcomes"]):
        require(dimension_valid(value, d))
        o = {k: integer(value.get(k), maximum) for k in FIELDS}
        require(sum(o.values()) == total//len(DIMENSIONS))
        outcomes.append({**d, **o})
    for k, expected in (("increased", c["increased"]), ("unchanged", c["unchanged"]), ("baseline", c["baseline"]), ("unknown", unknown)):
        require(sum(o[k] for o in outcomes) == expected)
    incomplete = any(j["state"] != "available" for j in jobs) or unknown > 0 or c["unexpected"] > 0
    state, reason = ("increased", "delayed_task_dispatch_increases_observed") if c["increased"] else ("baseline", "comparison_not_requested") if c["baseline"] else ("incomplete", "incomplete_delayed_task_dispatch_evidence") if incomplete else ("unchanged", "") if c["unchanged"] else ("unknown", "delayed_task_dispatch_evidence_unavailable")
    require(type(raw.get("incomplete")) is bool and raw["incomplete"] == incomplete and raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"scope": counters.SCOPE, "state": state, "counts": c, "outcomes": outcomes, "incomplete": incomplete}
    if "reason" in raw:
        out["reason"] = reason
    return out


def account(items):
    c = dict.fromkeys(COUNTS, 0)
    outcomes = [{**d, **dict.fromkeys(FIELDS, 0)} for d in DIMENSIONS]
    for item in items:
        for s, o in zip(item["signals"], outcomes):
            c["observed"] += "current" in s
            c["unexpected"] += item.get("unexpected", False)
            c[s["state"]] += 1
            o[s["state"] if s["state"] in FIELDS else "unknown"] += 1
    return c, outcomes


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
    items = []
    for item in raw["items"]:
        require(isinstance(item, dict) and isinstance(item.get("signals"), list) and len(item["signals"]) == len(DIMENSIONS))
        require(all(dimension_valid(s, d) for s, d in zip(item["signals"], DIMENSIONS)))
        items.append(counters.project_counter_item(item, base, DIMENSIONS))
    out["items"] = items
    out["omitted_items"] = integer(raw.get("omitted_items"), 2*(SAMPLES_MAX+NODES_MAX))
    c = out["counts"]
    require((len(items)+out["omitted_items"])*len(DIMENSIONS) == sum(c[k] for k in ("increased", "unchanged", "baseline", *counters.BAD)))
    keys = [i.get("target_id") or i["job"]+"/missing/"+i.get("node", "") for i in items]
    require(len(keys) == len(set(keys)))
    part, outcomes = account(items)
    for k in COUNTS[1:]:
        require(part[k] <= c[k] and (out["omitted_items"] or part[k] == c[k]))
    for p, f in zip(outcomes, out["outcomes"]):
        for k in FIELDS:
            require(p[k] <= f[k] and (out["omitted_items"] or p[k] == f[k]))
    return out



def fit(builds, report, maximum):
    h = builds.get("delayed_task_dispatch")
    while h and h["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode())+1 > maximum:
        h["items"].pop()
        h["omitted_items"] += 1
        builds["delayed_task_dispatch_envelope_omitted_items"] = builds.get("delayed_task_dispatch_envelope_omitted_items", 0)+1



def project_progress(raw, progress, times, current, events, role):
    from post_deployment_builds import require, timestamp
    require(role in ("single-box-v2", "control-plane-v2") and isinstance(raw, dict))
    out = {}
    for k in BOOLS:
        require(type(raw.get(k)) is bool)
        out[k] = raw[k]
    require(isinstance(raw.get("observed_outcomes"), list) and len(raw["observed_outcomes"]) <= len(DIMENSIONS))
    found = []
    for d in raw["observed_outcomes"]:
        require(isinstance(d, dict))
        canonical = {"outcome": d.get("outcome")}
        require(canonical in DIMENSIONS and canonical not in found)
        found.append(canonical)
    require(found == [d for d in DIMENSIONS if d in found])
    out["observed_outcomes"] = found
    require(bool(found) == out["observed_increases"])
    clocks = {k: timestamp(raw[k]) for k in ("first_increase_at", "last_increase_at") if k in raw}
    require(bool(clocks) == out["observed_increases"] and len(clocks) in (0,2))
    if clocks:
        require(times["first_observed_at"] <= clocks["first_increase_at"] <= clocks["last_increase_at"] <= times["last_observed_at"])
        out.update({k: raw[k] for k in clocks})
    for value in (current, *events):
        if not diagnostics.selected(value,"delayed_task_dispatch"):
            continue
        if value is not current and "delayed_task_dispatch" in value.get("omitted_sections", ()):
            continue
        h = value.get("delayed_task_dispatch")
        require(h is not None and not h["incomplete"] and h["state"] != "unknown" or out["observed_incomplete_evidence"])
        if not h:
            continue
        require(not h["counts"]["baseline"] or out["observed_baseline"])
        if h["counts"]["increased"]:
            at = timestamp(value["at"]) if "at" in value else times["last_observed_at"]
            require(clocks and clocks["first_increase_at"] <= at <= clocks["last_increase_at"])
        for o in h["outcomes"]:
            require(not o["increased"] or {"outcome":o["outcome"]} in found)
    if progress["observations"] == 0:
        require(not any(out[k] for k in BOOLS) and not found)
    if diagnostics.selected(current,"delayed_task_dispatch") and events and diagnostics.selected(events[-1],"delayed_task_dispatch") and "delayed_task_dispatch" not in events[-1].get("omitted_sections", ()):
        h = current.get("delayed_task_dispatch")
        latest = {k:h[k] for k in ("scope","state","reason","counts","outcomes","incomplete") if h and k in h} if h else None
        require(events[-1].get("delayed_task_dispatch") == latest)
    return out


def summary(builds):
    if not diagnostics.selected(builds,"delayed_task_dispatch"):
        return ""
    h = builds.get("delayed_task_dispatch")
    if h is None and builds.get("evidence",{}).get("expected_role") not in ("single-box-v2","control-plane-v2") and "delayed_task_dispatch_unavailable_reason" not in builds:
        return ""
    text = "\nDelayed-task dispatch outcomes (advisory): **"+(h["state"] if h else "unknown")+"**.\n\n"
    if h:
        text += ", ".join(k+"="+str(h["counts"][k]) for k in COUNTS)+".\n"
        for o in h["outcomes"]:
            text += "- "+o["outcome"]+": "+", ".join(k+"="+str(o[k]) for k in FIELDS)+"\n"
        text += "Counts classify process-local counter signals; exact target deltas count settled delayed-task dispatch attempts, not unique tasks or deliveries. Failed includes application errors and does not identify a platform incident. Retry does not establish its cause or a final result. Success does not prove unique or exactly-once execution. No new deployment gate or failure-rate threshold is inferred. Baselines, restarts, resets and missing samples establish no delta.\n"
    else:
        text += "Delayed-task dispatch evidence unavailable ("+builds.get("delayed_task_dispatch_unavailable_reason","delayed_task_dispatch_evidence_not_returned")+").\n"
    p = builds.get("progress",{}).get("delayed_task_dispatch")
    if p:
        text += "\nObserved Delayed-task dispatch history: "+", ".join(k+"="+str(p[k]).lower() for k in BOOLS)+".\n"
        for d in p["observed_outcomes"]:
            text += "- observed "+d["outcome"]+"\n"
        for k in ("first_increase_at","last_increase_at"):
            if k in p:
                text += "- "+k+": "+p[k]+"\n"
    return text
