"""Closed projection of durable image-preparation observations by replica scope."""

import datetime as dt
import post_deployment_diagnostics as diagnostics
import json
import re

from post_deployment_handoff import CLOCKS, PENDING_MAX, QUALITY, REASONS

PHASES = ("preparing", "layer_published", "scan_complete")
COUNTS = ("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected",
          "waiting_scopes", "empty_scopes", "unknown_age_scopes", "unsuccessful_collections", "enabled_scopes", "disabled_scopes")
PHASE_COUNTS = ("waiting_scopes", "empty_scopes", "unknown_age_scopes")


def project_summary(raw, base):
    from post_deployment_builds import require, integer, SAMPLES_MAX, NODES_MAX
    require(isinstance(raw, dict) and isinstance(raw.get("counts"), dict))
    require(base["expected_role"] in ("single-box-v2", "control-plane-v2"))
    jobs = [j for j in base["jobs"] if j["job"] == "imaged" and j["daemon"] == "imaged"]
    require(len(jobs) == 1)
    maximum = SAMPLES_MAX+NODES_MAX
    c = {k: integer(raw["counts"].get(k), maximum) for k in COUNTS}
    total = sum(c[k] for k in QUALITY)
    require(c["expected"] == jobs[0]["expected"] and c["expected"] <= total <= maximum)
    require(c["observed"] <= min(total, base["counts"]["observed"]) and c["unexpected"] <= c["observed"])
    require(c["waiting_scopes"]+c["empty_scopes"]+c["unsuccessful_collections"] <= c["observed"])
    require(c["unknown_age_scopes"] <= c["waiting_scopes"] and c["enabled_scopes"]+c["disabled_scopes"] <= c["observed"])
    require(c["complete"] <= min(c["enabled_scopes"]+c["disabled_scopes"], c["waiting_scopes"]+c["empty_scopes"]))
    require(isinstance(raw.get("phases"), list) and len(raw["phases"]) == len(PHASES))
    phases = []
    for name, value in zip(PHASES, raw["phases"]):
        require(isinstance(value, dict) and value.get("phase") == name)
        p = {k: integer(value.get(k), maximum) for k in PHASE_COUNTS}
        require(p["waiting_scopes"]+p["empty_scopes"]+c["unsuccessful_collections"] <= c["observed"])
        require(p["unknown_age_scopes"] <= min(p["waiting_scopes"], c["unknown_age_scopes"]))
        require(p["waiting_scopes"] <= c["waiting_scopes"] and p["empty_scopes"] >= c["empty_scopes"])
        require(p["waiting_scopes"]+p["empty_scopes"] >= c["complete"])
        phases.append({"phase": name, **p})
    require(sum(p["waiting_scopes"] for p in phases) >= c["waiting_scopes"])
    require(sum(p["unknown_age_scopes"] for p in phases) >= c["unknown_age_scopes"])
    incomplete = jobs[0]["state"] != "available" or any(c[k] for k in ("missing", "stale", "conflicting", "unavailable", "unexpected"))
    state, reason = ("waiting", "pending_preparations_observed") if c["waiting_scopes"] else ("incomplete", "incomplete_preparation_evidence") if incomplete else ("empty", "") if c["complete"] else ("unknown", "preparation_evidence_unavailable")
    require(type(raw.get("incomplete")) is bool and raw["incomplete"] == incomplete)
    require(raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"state": state, "counts": c, "phases": phases, "incomplete": incomplete}
    if "reason" in raw:
        out["reason"] = reason
    return out


def project_item(raw, base):
    from post_deployment_builds import require, integer, timestamp, MAX_AGE_SECONDS, CLOCK_SKEW_SECONDS, NODE_MAX_BYTES
    from post_deployment_process import number, UPTIME_MAX_SECONDS
    require(isinstance(raw, dict))
    now, after = timestamp(base["generated_at"]), timestamp(base["evidence_after"])
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
        require(scrape <= now+skew)
        out["scrape_observed_at"] = raw["scrape_observed_at"]

    def fresh(at):
        return at is not None and max(now-dt.timedelta(seconds=MAX_AGE_SECONDS), after-skew) <= at <= now+skew

    def origin(value, observed):
        if value is None:
            return None
        at = timestamp(value)
        require(now-dt.timedelta(seconds=UPTIME_MAX_SECONDS) <= at <= min(now+skew, observed+2*skew))
        return at

    def component(value, valid, phase=False):
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
            if phase:
                reasons["stale"] += ("collection_stale",)
                reasons["unavailable"] += ("collection_failed", "collection_not_observed", "collection_before_boundary")
            require(isinstance(state, str) and state in reasons and reason in reasons[state])
            require(value.get("values") is None and value.get("observed_at") is None)
        return result

    collection = component(raw.get("collection"), ("available",))
    prerequisite = None
    if collection["state"] == "available":
        v = raw["collection"]["values"]
        require(type(v.get("success")) is bool)
        at = origin(v.get("last_success_at"), timestamp(collection["observed_at"]))
        require(not v["success"] or at is not None)
        collection["values"] = {"success": v["success"]}
        if at is not None:
            collection["values"]["last_success_at"] = v["last_success_at"]
        if not v["success"]:
            prerequisite = ("unavailable", "collection_failed" if at is not None else "collection_not_observed")
        elif at < now-dt.timedelta(seconds=MAX_AGE_SECONDS):
            prerequisite = ("stale", "collection_stale")
        elif at < after-skew:
            prerequisite = ("unavailable", "collection_before_boundary")
    else:
        prerequisite = (collection["state"], collection.get("reason", ""))
    out["collection"] = collection
    observation = component(raw.get("observation"), ("available",))
    if observation["state"] == "available":
        enabled = raw["observation"]["values"].get("enabled")
        require(type(enabled) is bool)
        observation["values"] = {"enabled": enabled}
    out["observation"] = observation
    require(isinstance(raw.get("phases"), list) and len(raw["phases"]) == len(PHASES))
    phases = []
    for name, value in zip(PHASES, raw["phases"]):
        require(isinstance(value, dict) and value.get("phase") == name)
        p = component(value, ("waiting", "empty"), True)
        p["phase"] = name
        if prerequisite:
            require((p["state"], p.get("reason", "")) == prerequisite and "observed_at" not in p)
        else:
            require(p.get("reason", "") not in ("collection_failed", "collection_not_observed", "collection_before_boundary", "collection_stale"))
        if p["state"] in ("waiting", "empty"):
            require(prerequisite is None and timestamp(p["observed_at"]) == timestamp(collection["observed_at"]))
            v = value["values"]
            pending, unknown = integer(v.get("pending"), PENDING_MAX), integer(v.get("unknown_age"), PENDING_MAX)
            require(unknown <= pending and (p["state"] == "waiting") == (pending > 0))
            oldest = origin(v.get("oldest_phase_started_at"), timestamp(p["observed_at"]))
            values = {"pending": pending, "unknown_age": unknown}
            if pending == unknown:
                require(oldest is None and v.get("oldest_known_age_seconds") is None)
            else:
                age = number(v.get("oldest_known_age_seconds"))
                require(oldest is not None and abs(age-max((now-oldest).total_seconds(), 0)) <= 0.000001)
                values.update(oldest_phase_started_at=v["oldest_phase_started_at"], oldest_known_age_seconds=age)
            p["values"] = values
        phases.append(p)
    out["phases"] = phases
    expected = next((v["state"] for v in (collection, observation, *phases) if v["state"] in REASONS), "available")
    require(out["state"] == expected)
    return out


def project(raw, base):
    from post_deployment_builds import require, integer, PREVIEW_MAX, SAMPLES_MAX, NODES_MAX
    out = project_summary(raw, base)
    require(isinstance(raw.get("items"), list) and len(raw["items"]) <= PREVIEW_MAX)
    out["items"] = [project_item(item, base) for item in raw["items"]]
    out["omitted_items"] = integer(raw.get("omitted_items"), SAMPLES_MAX+NODES_MAX)
    require(sum(out["counts"][k] for k in QUALITY) == len(out["items"])+out["omitted_items"])
    keys = [i.get("target_id") or "missing/"+i.get("node", "") for i in out["items"]]
    require(len(set(keys)) == len(keys))
    c, phases = dict.fromkeys(COUNTS, 0), [{k: 0 for k in PHASE_COUNTS} for _ in PHASES]
    for item in out["items"]:
        c["observed"] += bool(item.get("target_id"))
        c["unexpected"] += item.get("unexpected", False)
        c["complete" if item["state"] == "available" else item["state"]] += 1
        c["waiting_scopes"] += any(p["state"] == "waiting" for p in item["phases"])
        c["empty_scopes"] += all(p["state"] == "empty" for p in item["phases"])
        c["unknown_age_scopes"] += any(p.get("values", {}).get("unknown_age", 0) > 0 for p in item["phases"])
        c["unsuccessful_collections"] += item["collection"].get("values", {}).get("success") is False
        if "values" in item["observation"]:
            c["enabled_scopes" if item["observation"]["values"]["enabled"] else "disabled_scopes"] += 1
        for count, p in zip(phases, item["phases"]):
            for k in ("waiting", "empty"):
                count[k+"_scopes"] += p["state"] == k
            count["unknown_age_scopes"] += p.get("values", {}).get("unknown_age", 0) > 0
    for k in COUNTS[1:]:
        require(c[k] <= out["counts"][k] and (out["omitted_items"] != 0 or c[k] == out["counts"][k]))
    for count, full in zip(phases, out["phases"]):
        for k in PHASE_COUNTS:
            require(count[k] <= full[k] and (out["omitted_items"] != 0 or count[k] == full[k]))
    return out


def project_progress(raw, progress, times, current, events, role):
    from post_deployment_builds import require
    from post_deployment_handoff import project_progress as waiting_progress
    # Both sections retain sampled waiting/empty observations, with no recovery claim.
    def adapt(value):
        value = diagnostics.remap(value,"preparation_health","handoff_health")
        if "preparation_health" in value.get("omitted_sections", ()):
            value["omitted_sections"] = ["handoff_health" if k == "preparation_health" else k for k in value["omitted_sections"]]
        h = value.get("preparation_health")
        return {"handoff_health": {k: v for k, v in h.items() if k != "phases"} if h else None, **{k: v for k, v in value.items() if k not in ("handoff_health", "preparation_health")}}
    require(isinstance(raw, dict) and type(raw.get("observed_unknown_ages")) is bool)
    out = waiting_progress(raw, progress, times, adapt(current), [adapt(e) for e in events], role)
    out["observed_unknown_ages"] = raw["observed_unknown_ages"]
    for value in (current, *events):
        if not diagnostics.selected(value,"preparation_health"):
            continue
        if value is not current and "preparation_health" in value.get("omitted_sections", ()):
            continue
        h = value.get("preparation_health")
        require(not h or not h["counts"]["unknown_age_scopes"] or out["observed_unknown_ages"])
    if diagnostics.selected(current,"preparation_health") and events and diagnostics.selected(events[-1],"preparation_health") and "preparation_health" not in events[-1].get("omitted_sections", ()):
        require(events[-1].get("preparation_health", {}).get("phases") == current.get("preparation_health", {}).get("phases"))
    require(not out["empty_throughout_observed_polls"] or not out["observed_unknown_ages"])
    return out


def fit(builds, report, maximum):
    h = builds.get("preparation_health")
    while h and h["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode())+1 > maximum:
        h["items"].pop()
        h["omitted_items"] += 1
        builds["preparation_envelope_omitted_items"] = builds.get("preparation_envelope_omitted_items", 0)+1


def summary(builds):
    if not diagnostics.selected(builds,"preparation_health"):
        return ""
    h = builds.get("preparation_health")
    if h is None and builds.get("evidence", {}).get("expected_role") not in ("single-box-v2", "control-plane-v2") and "preparation_health_unavailable_reason" not in builds:
        return ""
    text = "\nImage preparation (advisory): **"+(h["state"] if h else "unknown")+"**.\n\n"
    if h:
        text += ", ".join(k+"="+str(h["counts"][k]) for k in COUNTS)+".\n"
        for p in h["phases"]:
            text += "- "+p["phase"]+": "+", ".join(k+"="+str(p[k]) for k in PHASE_COUNTS)+"\n"
        text += "Scopes may overlap. Known phase age includes time waiting to resume; unknown ages remain explicit. No stall or successful deployment is inferred.\n"
        text += "Preparation preview omitted_items="+str(h["omitted_items"])+"; envelope_omitted_items="+str(builds.get("preparation_envelope_omitted_items", 0))+".\n"
    else:
        text += "Preparation evidence unavailable ("+builds.get("preparation_health_unavailable_reason", "preparation_evidence_not_returned")+").\n"
    progress = builds.get("progress", {})
    if "preparation_health" in progress:
        p = progress["preparation_health"]
        text += "\nObserved preparation history: "+", ".join(k+"="+str(p[k]).lower() for k in ("waiting_transitions", "empty_throughout_observed_polls", "observed_incomplete_evidence", "observed_unknown_ages"))+".\n"
        for k in CLOCKS:
            if k in p:
                text += "- preparation_"+k+": "+p[k]+"\n"
    if "preparation_health_unavailable_reason" in progress:
        text += "\nPreparation history unavailable ("+progress["preparation_health_unavailable_reason"]+").\n"
    return text
