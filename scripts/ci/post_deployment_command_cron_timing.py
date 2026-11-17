"""Closed projection of coherent cron admission timing counters."""
import post_deployment_diagnostics as diagnostics
import json

import post_deployment_app_task_operations as counters

COUNTS = counters.COUNTS
FIELDS = ("increased", "unchanged", "baseline", "unknown")
NAMES = ("le_1_seconds", "le_5_seconds", "le_15_seconds", "le_30_seconds", "le_60_seconds", "le_120_seconds", "le_300_seconds", "le_600_seconds", "le_1800_seconds", "le_3600_seconds", "le_inf", "timed", "unknown_timestamps")
DIMENSIONS = tuple({"signal": name} for name in NAMES)
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
    require(isinstance(raw.get("signals"), list) and len(raw["signals"]) == len(DIMENSIONS))
    outcomes = []
    for d, value in zip(DIMENSIONS, raw["signals"]):
        require(dimension_valid(value, d))
        o = {k: integer(value.get(k), maximum) for k in FIELDS}
        require(sum(o.values()) == total//len(DIMENSIONS))
        outcomes.append({**d, **o})
    for k, expected in (("increased", c["increased"]), ("unchanged", c["unchanged"]), ("baseline", c["baseline"]), ("unknown", unknown)):
        require(sum(o[k] for o in outcomes) == expected)
    incomplete = any(j["state"] != "available" for j in jobs) or unknown > 0 or c["unexpected"] > 0
    state, reason = ("increased", "cron_timing_increases_observed") if c["increased"] else ("baseline", "comparison_not_requested") if c["baseline"] else ("incomplete", "incomplete_cron_timing_evidence") if incomplete else ("unchanged", "") if c["unchanged"] else ("unknown", "cron_timing_evidence_unavailable")
    require(type(raw.get("incomplete")) is bool and raw["incomplete"] == incomplete and raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"scope": counters.SCOPE, "state": state, "counts": c, "signals": outcomes, "incomplete": incomplete}
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
        projected = counters.project_counter_item(item, base, DIMENSIONS)
        histogram_valid(projected["signals"])
        items.append(projected)
    out["items"] = items
    out["omitted_items"] = integer(raw.get("omitted_items"), 2*(SAMPLES_MAX+NODES_MAX))
    c = out["counts"]
    require((len(items)+out["omitted_items"])*len(DIMENSIONS) == sum(c[k] for k in ("increased", "unchanged", "baseline", *counters.BAD)))
    keys = [i.get("target_id") or i["job"]+"/missing/"+i.get("node", "") for i in items]
    require(len(keys) == len(set(keys)))
    part, outcomes = account(items)
    for k in COUNTS[1:]:
        require(part[k] <= c[k] and (out["omitted_items"] or part[k] == c[k]))
    for p, f in zip(outcomes, out["signals"]):
        for k in FIELDS:
            require(p[k] <= f[k] and (out["omitted_items"] or p[k] == f[k]))
    return out



def fit(builds, report, maximum):
    h = builds.get("command_cron_timing")
    while h and h["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode())+1 > maximum:
        h["items"].pop()
        h["omitted_items"] += 1
        builds["command_cron_timing_envelope_omitted_items"] = builds.get("command_cron_timing_envelope_omitted_items", 0)+1



def project_progress(raw, progress, times, current, events, role):
    from post_deployment_builds import require, timestamp
    require(role in ("single-box-v2", "control-plane-v2") and isinstance(raw, dict))
    out = {}
    for k in BOOLS:
        require(type(raw.get(k)) is bool)
        out[k] = raw[k]
    require(isinstance(raw.get("observed_signals"), list) and len(raw["observed_signals"]) <= len(DIMENSIONS))
    found = []
    for d in raw["observed_signals"]:
        require(isinstance(d, dict))
        canonical = {"signal": d.get("signal")}
        require(canonical in DIMENSIONS and canonical not in found)
        found.append(canonical)
    require(found == [d for d in DIMENSIONS if d in found])
    out["observed_signals"] = found
    require(bool(found) == out["observed_increases"])
    clocks = {k: timestamp(raw[k]) for k in ("first_increase_at", "last_increase_at") if k in raw}
    require(bool(clocks) == out["observed_increases"] and len(clocks) in (0,2))
    if clocks:
        require(times["first_observed_at"] <= clocks["first_increase_at"] <= clocks["last_increase_at"] <= times["last_observed_at"])
        out.update({k: raw[k] for k in clocks})
    for value in (current, *events):
        if not diagnostics.selected(value,"command_cron_timing"):
            continue
        if value is not current and "command_cron_timing" in value.get("omitted_sections", ()):
            continue
        h = value.get("command_cron_timing")
        require(h is not None and not h["incomplete"] and h["state"] != "unknown" or out["observed_incomplete_evidence"])
        if not h:
            continue
        require(not h["counts"]["baseline"] or out["observed_baseline"])
        if h["counts"]["increased"]:
            at = timestamp(value["at"]) if "at" in value else times["last_observed_at"]
            require(clocks and clocks["first_increase_at"] <= at <= clocks["last_increase_at"])
        for o in h["signals"]:
            require(not o["increased"] or {"signal":o["signal"]} in found)
    if progress["observations"] == 0:
        require(not any(out[k] for k in BOOLS) and not found)
    if diagnostics.selected(current,"command_cron_timing") and events and diagnostics.selected(events[-1],"command_cron_timing") and "command_cron_timing" not in events[-1].get("omitted_sections", ()):
        h = current.get("command_cron_timing")
        latest = {k:h[k] for k in ("scope","state","reason","counts","signals","incomplete") if h and k in h} if h else None
        require(events[-1].get("command_cron_timing") == latest)
    return out


def summary(builds):
    if not diagnostics.selected(builds,"command_cron_timing"):
        return ""
    h = builds.get("command_cron_timing")
    if h is None and builds.get("evidence",{}).get("expected_role") not in ("single-box-v2","control-plane-v2") and "command_cron_timing_unavailable_reason" not in builds:
        return ""
    text = "\nCommand-cron admission timing (advisory): **"+(h["state"] if h else "unknown")+"**.\n\n"
    if h:
        text += ", ".join(k+"="+str(h["counts"][k]) for k in COUNTS)+".\n"
        for o in h["signals"]:
            text += "- "+o["signal"]+": "+", ".join(k+"="+str(o[k]) for k in FIELDS)+"\n"
        text += "Counts classify process-local counter signals. Exact target deltas count timed observations, cumulative delay buckets, and unknown timestamps independently. Buckets overlap; do not sum them. Delay includes catch-up from the nominal schedule time and policy dispositions, not execution-start latency or continuous eligibility. Zero timed observations establish no zero-lag claim. No percentiles, unique task counts or deployment gates are inferred. Baselines, restarts, resets and incomplete histograms establish no timing delta.\n"
    else:
        text += "Cron timing evidence unavailable ("+builds.get("command_cron_timing_unavailable_reason","cron_timing_evidence_not_returned")+").\n"
    p = builds.get("progress",{}).get("command_cron_timing")
    if p:
        text += "\nObserved cron timing history: "+", ".join(k+"="+str(p[k]).lower() for k in BOOLS)+".\n"
        for d in p["observed_signals"]:
            text += "- observed "+d["signal"]+"\n"
        for k in ("first_increase_at","last_increase_at"):
            if k in p:
                text += "- "+k+": "+p[k]+"\n"
    return text


def histogram_valid(signals):
    from post_deployment_builds import require, timestamp
    for field in ("current", "previous", "delta"):
        values = [s.get(field) for s in signals[:-1]]
        if not any(v is not None for v in values):
            continue
        require(all(v is not None for v in values))
        if field != "delta":
            clocks = [timestamp(v["observed_at"]) for v in values]
            require(all(at == clocks[0] for at in clocks))
            values = [v["value"] for v in values]
        require(all(a <= b for a, b in zip(values, values[1:])) and values[-2] == values[-1])
