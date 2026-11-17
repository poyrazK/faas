"""Closed advisory readiness projection; never changes health qualification."""

import datetime as dt

REASONS = ("ready", "draining", "database", "vmmd", "grpc", "storage", "credentials", "stale", "process", "other")
COUNTS = ("ready", "not_ready", "missing", "stale", "conflicting", "unavailable")
CLOCKS = ("first_ready_at", "last_ready_at", "first_not_ready_at", "last_not_ready_at", "last_recovery_at", "last_loss_at")
DIAGNOSTICS = {
    "missing": ("expected_target_missing", "readiness_missing"),
    "stale": ("scrape_stale", "readiness_stale"),
    "conflicting": ("conflicting_targets", "conflicting_readiness"),
    "unavailable": ("source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "readiness_before_boundary"),
}


def project(raw, evidence, raw_items, generated, boundary):
    # Deferred import lets the build validator share its exact time/count rules.
    from post_deployment_builds import require, integer, timestamp, SAMPLES_MAX, NODES_MAX, MAX_AGE_SECONDS, CLOCK_SKEW_SECONDS
    require(isinstance(raw, dict))
    maximum = len(evidence["jobs"]) * (SAMPLES_MAX + NODES_MAX)
    require(isinstance(raw.get("counts"), dict))
    counts = {name: integer(raw["counts"].get(name), maximum) for name in COUNTS}
    total = sum(counts.values())
    require(total == len(evidence["items"]) + evidence["omitted_items"])
    incomplete = evidence["counts"]["unexpected"] > 0 or any(job["state"] != "available" for job in evidence["jobs"])
    if counts["not_ready"]:
        state, reason = "not_ready", "fresh_not_ready"
    elif incomplete or any(counts[name] for name in COUNTS[2:]):
        state, reason = "incomplete", "incomplete_readiness_evidence"
    elif counts["ready"]:
        state, reason = "ready", ""
    else:
        state, reason = "unknown", "readiness_unavailable"
    require(raw.get("state") == state and raw.get("reason", "") == reason)
    require(isinstance(raw.get("reasons"), list) and len(raw["reasons"]) <= len(REASONS))
    reasons, previous = [], -1
    for item in raw["reasons"]:
        require(isinstance(item, dict) and item.get("reason") in REASONS)
        index = REASONS.index(item["reason"])
        require(index > previous)
        previous = index
        count = integer(item.get("count"), total)
        require(count > 0)
        reasons.append({"reason": item["reason"], "count": count})
    require(sum(item["count"] for item in reasons if item["reason"] == "ready") == counts["ready"])
    require(sum(item["count"] for item in reasons if item["reason"] != "ready") == counts["not_ready"])
    preview, reason_counts, remaining = [], {}, dict(counts)
    skew = dt.timedelta(seconds=CLOCK_SKEW_SECONDS)
    for parent, raw_parent in zip(evidence["items"], raw_items):
        item = raw_parent.get("readiness")
        require(isinstance(item, dict))
        status, why = item.get("state"), item.get("reason")
        require(isinstance(status, str) and status in COUNTS and isinstance(why, str))
        remaining[status] -= 1
        require(remaining[status] >= 0)
        out = {"state": status, "reason": why}
        times = {}
        for field in ("observed_at", "reason_observed_at"):
            if field in item:
                times[field] = timestamp(item[field])
                require(times[field] <= generated + skew)
                out[field] = item[field]
        if status in ("ready", "not_ready"):
            require(why in REASONS and (status == "ready") == (why == "ready"))
            require(bool(parent.get("target_id")) and len(times) == 2 and "scrape_observed_at" in parent)
            require(all(at >= generated - dt.timedelta(seconds=MAX_AGE_SECONDS) and at >= boundary - skew
                        for at in (*times.values(), timestamp(parent["scrape_observed_at"]))))
            reason_counts[why] = reason_counts.get(why, 0) + 1
        else:
            require(why in DIAGNOSTICS[status])
        preview.append(out)
    require(all(any(item["reason"] == reason and item["count"] >= count for item in reasons)
                for reason, count in reason_counts.items()))
    out = {"state": state, "counts": counts, "reasons": reasons}
    if "reason" in raw:
        out["reason"] = reason
    return out, preview


def project_progress(raw, progress, times, current, events):
    from post_deployment_builds import require, integer, timestamp
    require(isinstance(raw, dict))
    out = {"losses": integer(raw.get("losses"), progress["observations"])}
    for field in ("ready_throughout_observed_polls", "observed_incomplete_evidence"):
        require(type(raw.get(field)) is bool)
        out[field] = raw[field]
    clocks = {}
    for field in CLOCKS:
        if field in raw:
            clocks[field], out[field] = timestamp(raw[field]), raw[field]
    if progress["observations"] == 0:
        require(not clocks and not any(out.values()))
        return out
    first, last = times["first_observed_at"], times["last_observed_at"]
    require(all(first <= at <= last for at in clocks.values()))
    for a, b in (("first_ready_at", "last_ready_at"), ("first_not_ready_at", "last_not_ready_at")):
        require((a in clocks) == (b in clocks))
        if a in clocks:
            require(clocks[a] <= clocks[b])
    require(bool(out["losses"]) == ("last_loss_at" in clocks))
    if out["losses"]:
        require("first_ready_at" in clocks and "last_not_ready_at" in clocks)
        require(clocks["first_ready_at"] <= clocks["last_loss_at"] <= clocks["last_not_ready_at"])
    if "last_recovery_at" in clocks:
        require("first_not_ready_at" in clocks and "last_ready_at" in clocks)
        require(clocks["first_not_ready_at"] <= clocks["last_recovery_at"] <= clocks["last_ready_at"])
    evidence = current.get("evidence", {})
    readiness = evidence.get("readiness", {})
    state = readiness.get("state", "unknown")
    if state == "ready":
        require(clocks.get("last_ready_at") == last)
    if state == "not_ready":
        require(clocks.get("last_not_ready_at") == last)
    incomplete = state in ("unknown", "incomplete") or evidence.get("counts", {}).get("unexpected", 0) or any(readiness.get("counts", {}).get(name, 0) for name in COUNTS[2:])
    incomplete = incomplete or any(job["state"] != "available" for job in evidence.get("jobs", []))
    require(not incomplete or out["observed_incomplete_evidence"])
    if out["ready_throughout_observed_polls"]:
        require(state == "ready" and not out["observed_incomplete_evidence"] and "first_not_ready_at" not in clocks)
        require(clocks.get("first_ready_at") == first and clocks.get("last_ready_at") == last)
    for event in events:
        r = event.get("readiness", {})
        status, at = r.get("state", "unknown"), timestamp(event["at"])
        if status in ("ready", "not_ready"):
            key = "ready" if status == "ready" else "not_ready"
            require("first_" + key + "_at" in clocks)
            require(clocks["first_" + key + "_at"] <= at <= clocks["last_" + key + "_at"])
        require(not out["ready_throughout_observed_polls"] or status == "ready")
        gap = status in ("unknown", "incomplete") or event.get("counts", {}).get("unexpected", 0) or any(r.get("counts", {}).get(name, 0) for name in COUNTS[2:])
        gap = gap or any(job["state"] != "available" for job in event.get("jobs", []))
        require(not gap or out["observed_incomplete_evidence"])
    if events:
        require(events[-1].get("readiness") == evidence.get("readiness"))
    return out


def summary(evidence, progress=None):
    text = "\nRuntime readiness (advisory): **" + evidence.get("readiness", {}).get("state", "unknown") + "**.\n\n"
    if "readiness" in evidence:
        r = evidence["readiness"]
        text += ", ".join(name + "=" + str(r["counts"][name]) for name in COUNTS) + ".\n\n"
        if r["reasons"]:
            text += "Readiness reasons: " + ", ".join(item["reason"] + "=" + str(item["count"]) for item in r["reasons"]) + ".\n"
    else:
        text += "Readiness evidence unavailable (" + evidence.get("readiness_unavailable_reason", "readiness_not_returned") + ").\n"
    if progress and "readiness" in progress:
        p = progress["readiness"]
        text += "\nObserved readiness: losses=" + str(p["losses"]) + ", ready_throughout_observed_polls=" + str(p["ready_throughout_observed_polls"]).lower() + ", observed_incomplete_evidence=" + str(p["observed_incomplete_evidence"]).lower() + ".\n"
        for clock in CLOCKS:
            if clock in p:
                text += "- readiness_" + clock + ": " + p[clock] + "\n"
    if progress and "readiness_unavailable_reason" in progress:
        text += "\nReadiness history unavailable (" + progress["readiness_unavailable_reason"] + ").\n"
    return text
