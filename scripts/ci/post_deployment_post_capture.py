"""Closed projection of durable post-capture observations by replica scope."""

import datetime as dt
import post_deployment_diagnostics as diagnostics
import json
import re

from post_deployment_handoff import PENDING_MAX, QUALITY, REASONS

CLOCKS = ("first_nonempty_at", "last_nonempty_at", "first_empty_at", "last_empty_at")

CLASSES = ("publication_pending", "activation_pending", "dependency_held", "compatibility_held", "hosting_verifying", "hosting_retry", "gate_overdue", "unknown")
SCOPE = "replica_observations_of_shared_fleet"
COUNTS = ("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected",
          "nonempty_views", "empty_views", "unknown_age_views", "unsuccessful_collections", "enabled_views", "disabled_views")
CLASS_COUNTS = ("nonempty_views", "empty_views", "unknown_age_views")


def project_summary(raw, base):
    from post_deployment_builds import require, integer, SAMPLES_MAX, NODES_MAX
    require(isinstance(raw, dict) and isinstance(raw.get("counts"), dict) and raw.get("scope") == SCOPE)
    require(base["expected_role"] in ("single-box-v2", "control-plane-v2"))
    names = jobs_for_role(base["expected_role"])
    jobs = [j for j in base["jobs"] if j["job"] in names and j["daemon"] == "imaged"]
    require(len(jobs) == len(names) and {j["job"] for j in jobs} == set(names))
    maximum = len(names)*(SAMPLES_MAX+NODES_MAX)
    c = {k: integer(raw["counts"].get(k), maximum) for k in COUNTS}
    total = sum(c[k] for k in QUALITY)
    require(c["expected"] == sum(j["expected"] for j in jobs) and c["expected"] <= total <= maximum)
    require(c["observed"] <= min(total, base["counts"]["observed"]) and c["unexpected"] <= c["observed"])
    require(c["nonempty_views"]+c["empty_views"]+c["unsuccessful_collections"] <= c["observed"])
    require(c["unknown_age_views"] <= c["nonempty_views"] and c["enabled_views"]+c["disabled_views"] <= c["observed"])
    require(c["complete"] <= min(c["enabled_views"]+c["disabled_views"], c["nonempty_views"]+c["empty_views"]))
    require(isinstance(raw.get("classes"), list) and len(raw["classes"]) == len(CLASSES))
    classes = []
    for name, value in zip(CLASSES, raw["classes"]):
        require(isinstance(value, dict) and value.get("class") == name)
        p = {k: integer(value.get(k), maximum) for k in CLASS_COUNTS}
        require(p["nonempty_views"]+p["empty_views"]+c["unsuccessful_collections"] <= c["observed"])
        require(p["unknown_age_views"] <= min(p["nonempty_views"], c["unknown_age_views"]))
        require(p["nonempty_views"] <= c["nonempty_views"] and p["empty_views"] >= c["empty_views"])
        require(p["nonempty_views"]+p["empty_views"] >= c["complete"])
        require(name != "unknown" or p["nonempty_views"] == p["unknown_age_views"])
        classes.append({"class": name, **p})
    require(sum(p["nonempty_views"] for p in classes) >= c["nonempty_views"])
    require(sum(p["unknown_age_views"] for p in classes) >= c["unknown_age_views"])
    incomplete = any(j["state"] != "available" for j in jobs) or any(c[k] for k in ("missing", "stale", "conflicting", "unavailable", "unexpected"))
    state, reason = ("nonempty", "post_capture_evidence_observed") if c["nonempty_views"] else ("incomplete", "incomplete_post_capture_evidence") if incomplete else ("empty", "") if c["complete"] else ("unknown", "post_capture_evidence_unavailable")
    require(type(raw.get("incomplete")) is bool and raw["incomplete"] == incomplete)
    require(raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"scope": SCOPE, "state": state, "counts": c, "classes": classes, "incomplete": incomplete}
    if "reason" in raw:
        out["reason"] = reason
    return out


def project_item(raw, base):
    from post_deployment_builds import require, integer, timestamp, MAX_AGE_SECONDS, CLOCK_SKEW_SECONDS, NODE_MAX_BYTES
    from post_deployment_process import number, UPTIME_MAX_SECONDS
    require(isinstance(raw, dict))
    now, after = timestamp(base["generated_at"]), timestamp(base["evidence_after"])
    skew = dt.timedelta(seconds=CLOCK_SKEW_SECONDS)
    require(raw.get("job") in jobs_for_role(base["expected_role"]))
    out = {"state": raw.get("state"), "job": raw["job"]}
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

    def component(value, valid, cohort=False):
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
            if cohort:
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
    require(isinstance(raw.get("classes"), list) and len(raw["classes"]) == len(CLASSES))
    classes = []
    for name, value in zip(CLASSES, raw["classes"]):
        require(isinstance(value, dict) and value.get("class") == name)
        p = component(value, ("nonempty", "empty"), True)
        p["class"] = name
        if prerequisite:
            require((p["state"], p.get("reason", "")) == prerequisite and "observed_at" not in p)
        else:
            require(p.get("reason", "") not in ("collection_failed", "collection_not_observed", "collection_before_boundary", "collection_stale"))
        if p["state"] in ("nonempty", "empty"):
            require(prerequisite is None and timestamp(p["observed_at"]) == timestamp(collection["observed_at"]))
            v = value["values"]
            pending, unknown = integer(v.get("pending"), PENDING_MAX), integer(v.get("unknown_age"), PENDING_MAX)
            require(unknown <= pending and (p["state"] == "nonempty") == (pending > 0))
            require(name != "unknown" or pending == unknown)
            oldest = origin(v.get("oldest_boundary_at"), timestamp(p["observed_at"]))
            values = {"pending": pending, "unknown_age": unknown}
            if pending == unknown:
                require(oldest is None and v.get("oldest_known_age_seconds") is None)
            else:
                age = number(v.get("oldest_known_age_seconds"))
                require(oldest is not None and abs(age-max((now-oldest).total_seconds(), 0)) <= 0.000001)
                values.update(oldest_boundary_at=v["oldest_boundary_at"], oldest_known_age_seconds=age)
            p["values"] = values
        classes.append(p)
    out["classes"] = classes
    expected = next((v["state"] for v in (collection, observation, *classes) if v["state"] in REASONS), "available")
    require(out["state"] == expected)
    return out


def project(raw, base):
    from post_deployment_builds import require, integer, PREVIEW_MAX, SAMPLES_MAX, NODES_MAX
    out = project_summary(raw, base)
    require(isinstance(raw.get("items"), list) and len(raw["items"]) <= PREVIEW_MAX)
    out["items"] = [project_item(item, base) for item in raw["items"]]
    out["omitted_items"] = integer(raw.get("omitted_items"), len(jobs_for_role(base["expected_role"]))*(SAMPLES_MAX+NODES_MAX))
    require(sum(out["counts"][k] for k in QUALITY) == len(out["items"])+out["omitted_items"])
    keys = [i.get("target_id") or i["job"]+"/missing/"+i.get("node", "") for i in out["items"]]
    require(len(set(keys)) == len(keys))
    c, classes = dict.fromkeys(COUNTS, 0), [{k: 0 for k in CLASS_COUNTS} for _ in CLASSES]
    for item in out["items"]:
        c["observed"] += bool(item.get("target_id"))
        c["unexpected"] += item.get("unexpected", False)
        c["complete" if item["state"] == "available" else item["state"]] += 1
        c["nonempty_views"] += any(p["state"] == "nonempty" for p in item["classes"])
        c["empty_views"] += all(p["state"] == "empty" for p in item["classes"])
        c["unknown_age_views"] += any(p.get("values", {}).get("unknown_age", 0) > 0 for p in item["classes"])
        c["unsuccessful_collections"] += item["collection"].get("values", {}).get("success") is False
        if "values" in item["observation"]:
            c["enabled_views" if item["observation"]["values"]["enabled"] else "disabled_views"] += 1
        for count, p in zip(classes, item["classes"]):
            for k in ("nonempty", "empty"):
                count[k+"_views"] += p["state"] == k
            count["unknown_age_views"] += p.get("values", {}).get("unknown_age", 0) > 0
    for k in COUNTS[1:]:
        require(c[k] <= out["counts"][k] and (out["omitted_items"] != 0 or c[k] == out["counts"][k]))
    for count, full in zip(classes, out["classes"]):
        for k in CLASS_COUNTS:
            require(count[k] <= full[k] and (out["omitted_items"] != 0 or count[k] == full[k]))
    return out


def project_progress(raw, progress, times, current, events, role):
    from post_deployment_builds import require
    from post_deployment_handoff import project_progress as observed_progress
    # Reuse only sampled empty/nonempty bookkeeping, never infer recovery.
    def adapt(value):
        value = diagnostics.remap(value,"post_capture_health","handoff_health")
        if "post_capture_health" in value.get("omitted_sections", ()):
            value["omitted_sections"] = ["handoff_health" if k == "post_capture_health" else k for k in value["omitted_sections"]]
        h = value.get("post_capture_health")
        mapped = None
        if h:
            mapped = {k: v for k, v in h.items() if k not in ("classes", "scope")}
            mapped["state"] = "waiting" if h["state"] == "nonempty" else h["state"]
            mapped["counts"] = {("waiting_scopes" if k == "nonempty_views" else k): v for k, v in h["counts"].items()}
        return {"handoff_health": mapped, **{k: v for k, v in value.items() if k not in ("handoff_health", "post_capture_health")}}
    require(role in ("single-box-v2", "control-plane-v2"))
    require(isinstance(raw, dict) and type(raw.get("observed_unknown_ages")) is bool)
    renamed = {"waiting_transitions": raw.get("nonempty_transitions"),
               "empty_throughout_observed_polls": raw.get("empty_throughout_observed_polls"),
               "observed_incomplete_evidence": raw.get("observed_incomplete_evidence")}
    for key in CLOCKS:
        if key in raw:
            renamed[key.replace("nonempty", "waiting")] = raw[key]
    out = observed_progress(renamed, progress, times, adapt(current), [adapt(e) for e in events], role)
    out = {k.replace("waiting", "nonempty"): v for k, v in out.items()}
    out["observed_unknown_ages"] = raw["observed_unknown_ages"]
    for value in (current, *events):
        if not diagnostics.selected(value,"post_capture_health"):
            continue
        if value is not current and "post_capture_health" in value.get("omitted_sections", ()):
            continue
        h = value.get("post_capture_health")
        require(not h or not h["counts"]["unknown_age_views"] or out["observed_unknown_ages"])
    if diagnostics.selected(current,"post_capture_health") and events and diagnostics.selected(events[-1],"post_capture_health") and "post_capture_health" not in events[-1].get("omitted_sections", ()):
        require(events[-1].get("post_capture_health", {}).get("classes") == current.get("post_capture_health", {}).get("classes"))
    require(not out["empty_throughout_observed_polls"] or not out["observed_unknown_ages"])
    return out


def fit(builds, report, maximum):
    h = builds.get("post_capture_health")
    while h and h["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode())+1 > maximum:
        h["items"].pop()
        h["omitted_items"] += 1
        builds["post_capture_envelope_omitted_items"] = builds.get("post_capture_envelope_omitted_items", 0)+1


def summary(builds):
    if not diagnostics.selected(builds,"post_capture_health"):
        return ""
    h = builds.get("post_capture_health")
    if h is None and builds.get("evidence", {}).get("expected_role") not in ("single-box-v2", "control-plane-v2") and "post_capture_health_unavailable_reason" not in builds:
        return ""
    text = "\nPost-capture activation (advisory): **"+(h["state"] if h else "unknown")+"**.\n\n"
    if h:
        text += ", ".join(k+"="+str(h["counts"][k]) for k in COUNTS)+".\n"
        for p in h["classes"]:
            text += "- "+p["class"]+": "+", ".join(k+"="+str(p[k]) for k in CLASS_COUNTS)+"\n"
        text += "Each replica observes the shared fleet; compare views without summing deployment counts. Known ages measure capture, publication or retained gate/verification boundaries, not continuous time in a class. Gate evidence remains advisory; no new health or eligibility verdict is inferred.\n"
        text += "Post-capture preview omitted_items="+str(h["omitted_items"])+"; envelope_omitted_items="+str(builds.get("post_capture_envelope_omitted_items", 0))+".\n"
    else:
        text += "Post-capture evidence unavailable ("+builds.get("post_capture_health_unavailable_reason", "post_capture_evidence_not_returned")+").\n"
    progress = builds.get("progress", {})
    if "post_capture_health" in progress:
        p = progress["post_capture_health"]
        text += "\nObserved post-capture history: "+", ".join(k+"="+str(p[k]).lower() for k in ("nonempty_transitions", "empty_throughout_observed_polls", "observed_incomplete_evidence", "observed_unknown_ages"))+".\n"
        for k in CLOCKS:
            if k in p:
                text += "- post_capture_"+k+": "+p[k]+"\n"
    if "post_capture_health_unavailable_reason" in progress:
        text += "\nPost-capture history unavailable ("+progress["post_capture_health_unavailable_reason"]+").\n"
    return text


def jobs_for_role(role):
    return ("imaged",) if role in ("single-box-v2", "control-plane-v2") else ()
