"""Closed advisory build-claim and cleanup-obligation replica observations."""

import datetime as dt
import post_deployment_diagnostics as diagnostics
import json
import re

from post_deployment_handoff import PENDING_MAX, QUALITY, REASONS

SCOPE = "replica_observations_of_shared_fleet"
COUNTS = ("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected",
          "nonempty_views", "empty_views", "running_views", "cleanup_views", "retried_cleanup_views", "unsuccessful_collections")
CLOCKS = ("first_nonempty_at", "last_nonempty_at", "first_empty_at", "last_empty_at")


def project_summary(raw, base):
    from post_deployment_builds import require, integer, SAMPLES_MAX, NODES_MAX
    require(isinstance(raw, dict) and raw.get("scope") == SCOPE and isinstance(raw.get("counts"), dict))
    require(base["expected_role"] in ("single-box-v2", "control-plane-v2"))
    jobs = [j for j in base["jobs"] if j["job"] == "apid" and j["daemon"] == "apid"]
    require(len(jobs) == 1)
    maximum = SAMPLES_MAX+NODES_MAX
    c = {k: integer(raw["counts"].get(k), maximum) for k in COUNTS}
    total = sum(c[k] for k in QUALITY)
    require(c["expected"] == jobs[0]["expected"] and c["expected"] <= total <= maximum)
    require(c["observed"] <= min(total, base["counts"]["observed"]) and c["unexpected"] <= c["observed"])
    require(c["nonempty_views"]+c["empty_views"]+c["unsuccessful_collections"] <= c["observed"])
    require(c["complete"] <= c["nonempty_views"]+c["empty_views"] and c["retried_cleanup_views"] <= c["cleanup_views"])
    require(max(c["running_views"], c["cleanup_views"]) <= c["nonempty_views"] <= c["running_views"]+c["cleanup_views"])
    incomplete = jobs[0]["state"] != "available" or any(c[k] for k in ("missing", "stale", "conflicting", "unavailable", "unexpected"))
    state, reason = ("nonempty", "build_recovery_evidence_observed") if c["nonempty_views"] else ("incomplete", "incomplete_build_recovery_evidence") if incomplete else ("empty", "") if c["complete"] else ("unknown", "build_recovery_evidence_unavailable")
    require(type(raw.get("incomplete")) is bool and raw["incomplete"] == incomplete)
    require(raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"scope": SCOPE, "state": state, "counts": c, "incomplete": incomplete}
    if "reason" in raw:
        out["reason"] = reason
    return out


def project_item(raw, base):
    from post_deployment_builds import require, integer, timestamp, MAX_AGE_SECONDS, CLOCK_SKEW_SECONDS, NODE_MAX_BYTES
    from post_deployment_process import number, UPTIME_MAX_SECONDS
    require(isinstance(raw, dict) and raw.get("job") == "apid")
    now, after = timestamp(base["generated_at"]), timestamp(base["evidence_after"])
    skew = dt.timedelta(seconds=CLOCK_SKEW_SECONDS)
    out = {"job": "apid", "state": raw.get("state")}
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

    def component(value, valid, work=False):
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
            if work:
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
    for name, count_name, time_name in (("running", "count", "oldest_started_at"), ("cleanup", "pending", "oldest_created_at")):
        value = raw.get(name)
        result = component(value, ("nonempty", "empty"), True)
        if prerequisite:
            require((result["state"], result.get("reason", "")) == prerequisite and "observed_at" not in result)
        else:
            require(result.get("reason", "") not in ("collection_failed", "collection_not_observed", "collection_before_boundary", "collection_stale"))
        if result["state"] in ("nonempty", "empty"):
            require(prerequisite is None and timestamp(result["observed_at"]) == timestamp(collection["observed_at"]))
            v = value["values"]
            count = integer(v.get(count_name), PENDING_MAX)
            oldest = origin(v.get(time_name), timestamp(result["observed_at"]))
            require((result["state"] == "nonempty") == (count > 0))
            values = {count_name: count}
            if name == "cleanup":
                values["retried"] = integer(v.get("retried"), PENDING_MAX)
                require(values["retried"] <= count)
            if count == 0:
                require(oldest is None and v.get("oldest_age_seconds") is None)
            else:
                age = number(v.get("oldest_age_seconds"))
                require(oldest is not None and abs(age-max((now-oldest).total_seconds(), 0)) <= 0.000001)
                values.update({time_name: v[time_name], "oldest_age_seconds": age})
            result["values"] = values
        out[name] = result
    expected = next((v["state"] for v in (collection, out["running"], out["cleanup"]) if v["state"] in REASONS), "available")
    require(out["state"] == expected)
    return out


def project(raw, base):
    from post_deployment_builds import require, integer, PREVIEW_MAX, SAMPLES_MAX, NODES_MAX
    out = project_summary(raw, base)
    require(isinstance(raw.get("items"), list) and len(raw["items"]) <= PREVIEW_MAX)
    out["items"] = [project_item(i, base) for i in raw["items"]]
    out["omitted_items"] = integer(raw.get("omitted_items"), SAMPLES_MAX+NODES_MAX)
    require(sum(out["counts"][k] for k in QUALITY) == len(out["items"])+out["omitted_items"])
    keys = [i.get("target_id") or "apid/missing/"+i.get("node", "") for i in out["items"]]
    require(len(keys) == len(set(keys)))
    c = dict.fromkeys(COUNTS, 0)
    for i in out["items"]:
        running, cleanup = i["running"]["state"] == "nonempty", i["cleanup"]["state"] == "nonempty"
        c["observed"] += bool(i.get("target_id"))
        c["unexpected"] += i.get("unexpected", False)
        c["complete" if i["state"] == "available" else i["state"]] += 1
        c["running_views"] += running
        c["cleanup_views"] += cleanup
        c["nonempty_views"] += running or cleanup
        c["empty_views"] += i["running"]["state"] == i["cleanup"]["state"] == "empty"
        c["retried_cleanup_views"] += i["cleanup"].get("values", {}).get("retried", 0) > 0
        c["unsuccessful_collections"] += i["collection"].get("values", {}).get("success") is False
    for k in COUNTS[1:]:
        require(c[k] <= out["counts"][k] and (out["omitted_items"] != 0 or c[k] == out["counts"][k]))
    return out


def project_progress(raw, progress, times, current, events, role):
    from post_deployment_builds import require
    from post_deployment_handoff import project_progress as observed_progress

    def adapt(value):
        value = diagnostics.remap(value,"build_recovery_health","handoff_health")
        if "build_recovery_health" in value.get("omitted_sections", ()):
            value["omitted_sections"] = ["handoff_health" if k == "build_recovery_health" else k for k in value["omitted_sections"]]
        h = value.get("build_recovery_health")
        mapped = None
        if h:
            mapped = {k: v for k, v in h.items() if k != "scope"}
            mapped["state"] = "waiting" if h["state"] == "nonempty" else h["state"]
            mapped["counts"] = {("waiting_scopes" if k == "nonempty_views" else k): v for k, v in h["counts"].items()}
        return {"handoff_health": mapped, **{k: v for k, v in value.items() if k not in ("handoff_health", "build_recovery_health")}}

    require(role in ("single-box-v2", "control-plane-v2"))
    require(isinstance(raw, dict))
    renamed = {"waiting_transitions": raw.get("nonempty_transitions"),
               "empty_throughout_observed_polls": raw.get("empty_throughout_observed_polls"),
               "observed_incomplete_evidence": raw.get("observed_incomplete_evidence")}
    for k in CLOCKS:
        if k in raw:
            renamed[k.replace("nonempty", "waiting")] = raw[k]
    out = observed_progress(renamed, progress, times, adapt(current), [adapt(e) for e in events], role)
    return {k.replace("waiting", "nonempty"): v for k, v in out.items()}


def fit(builds, report, maximum):
    h = builds.get("build_recovery_health")
    while h and h["items"] and len(json.dumps(report, allow_nan=False, indent=2).encode())+1 > maximum:
        h["items"].pop()
        h["omitted_items"] += 1
        builds["build_recovery_envelope_omitted_items"] = builds.get("build_recovery_envelope_omitted_items", 0)+1


def summary(builds):
    if not diagnostics.selected(builds,"build_recovery_health"):
        return ""
    h = builds.get("build_recovery_health")
    if h is None and builds.get("evidence", {}).get("expected_role") not in ("single-box-v2", "control-plane-v2") and "build_recovery_health_unavailable_reason" not in builds:
        return ""
    text = "\nBuild recovery and VM cleanup (advisory): **"+(h["state"] if h else "unknown")+"**.\n\n"
    if h:
        text += ", ".join(k+"="+str(h["counts"][k]) for k in COUNTS)+".\n"
        text += "Each apid observes the shared fleet; compare replica views without summing builds or cleanup obligations. Cleanup age begins at obligation creation and survives retries. Recorded failures are a subset of outstanding obligations.\n"
        text += "Build-recovery preview omitted_items="+str(h["omitted_items"])+"; envelope_omitted_items="+str(builds.get("build_recovery_envelope_omitted_items", 0))+".\n"
    else:
        text += "Build-recovery evidence unavailable ("+builds.get("build_recovery_health_unavailable_reason", "build_recovery_evidence_not_returned")+").\n"
    progress = builds.get("progress", {})
    if "build_recovery_health" in progress:
        p = progress["build_recovery_health"]
        text += "\nObserved build-recovery history: "+", ".join(k+"="+str(p[k]).lower() for k in ("nonempty_transitions", "empty_throughout_observed_polls", "observed_incomplete_evidence"))+".\n"
        for k in CLOCKS:
            if k in p:
                text += "- build_recovery_"+k+": "+p[k]+"\n"
    if "build_recovery_health_unavailable_reason" in progress:
        text += "\nBuild-recovery history unavailable ("+progress["build_recovery_health_unavailable_reason"]+").\n"
    return text
