"""Closed advisory uptime/continuity projection for deployment artifacts."""

import datetime as dt
import math
import re

COUNTS = ("fresh", "missing", "stale", "conflicting", "unavailable", "compared", "changed", "unchanged",
          "same_revision_changes", "different_revision_changes", "unknown_revision_changes", "new_targets",
          "removed_targets", "repeated_samples", "comparison_unavailable")
PROGRESS = ("polls_with_comparison", "compared_pairs", "observed_changes", "same_revision_changes",
            "different_revision_changes", "unknown_revision_changes", "new_targets", "removed_targets")
LOOKBACK_SECONDS = 600
UPTIME_MAX_SECONDS = 315360000
START_TOLERANCE_SECONDS = 31
RESET_TOLERANCE_SECONDS = 1
DIAGNOSTICS = {
    "missing": ("expected_target_missing", "uptime_missing"),
    "stale": ("scrape_stale", "uptime_stale"),
    "conflicting": ("conflicting_targets", "conflicting_uptime"),
    "unavailable": ("source_unavailable", "invalid_evidence", "invalid_sample_time", "scrape_before_boundary", "scrape_down", "uptime_before_boundary", "invalid_uptime"),
}


def number(value):
    from post_deployment_builds import require
    require(type(value) in (int, float) and math.isfinite(value) and 0 <= value <= UPTIME_MAX_SECONDS)
    return value


def sample_ns(value):
    from post_deployment_builds import timestamp
    clock = timestamp(value).replace(microsecond=0) - dt.datetime(1970, 1, 1, tzinfo=dt.timezone.utc)
    fraction = re.search(r"T\d{2}:\d{2}:\d{2}(?:\.(\d{1,9}))?", value)[1] or ""
    return (clock.days * 86400 + clock.seconds) * 1000000000 + int(fraction.ljust(9, "0"))


def compare(uptime, at, previous_uptime, previous_at):
    current, previous = sample_ns(at), sample_ns(previous_at)
    if current == previous and uptime == previous_uptime:
        return "repeated_sample", ""
    if current <= previous:
        return "unavailable", "sample_clock_regressed"
    drift = (current-previous)/1000000000 - (uptime-previous_uptime)
    if uptime + RESET_TOLERANCE_SECONDS < previous_uptime or drift > START_TOLERANCE_SECONDS:
        return "changed", ""
    if drift < -START_TOLERANCE_SECONDS:
        return "unavailable", "start_estimate_regressed"
    return "unchanged", ""


def fresh_time(value, now, boundary):
    from post_deployment_builds import require, timestamp, MAX_AGE_SECONDS, CLOCK_SKEW_SECONDS
    at = timestamp(value)
    require(now-dt.timedelta(seconds=MAX_AGE_SECONDS) <= at <= now+dt.timedelta(seconds=CLOCK_SKEW_SECONDS))
    require(at >= boundary-dt.timedelta(seconds=CLOCK_SKEW_SECONDS))
    return value


def project_item(raw, parent, generated, boundary, comparison_at):
    from post_deployment_builds import require
    require(isinstance(raw, dict))
    state, reason = raw.get("state"), raw.get("reason", "")
    out = {"state": state}
    if "reason" in raw:
        out["reason"] = reason
    if state != "fresh":
        require(isinstance(state, str) and state in DIAGNOSTICS and reason in DIAGNOSTICS[state])
        require(not any(name in raw for name in ("uptime_seconds", "observed_at", "comparison", "comparison_reason", "previous_uptime_seconds", "previous_observed_at", "revision_relation")))
        return out
    require(reason == "" and bool(parent.get("target_id")))
    fresh_time(parent.get("scrape_observed_at"), generated, boundary)
    out["uptime_seconds"] = number(raw.get("uptime_seconds"))
    out["observed_at"] = fresh_time(raw.get("observed_at"), generated, boundary)
    state, why, relation = raw.get("comparison"), raw.get("comparison_reason", ""), raw.get("revision_relation", "")
    out["comparison"] = state
    if "comparison_reason" in raw:
        out["comparison_reason"] = why
    if "revision_relation" in raw:
        out["revision_relation"] = relation
    if comparison_at is None:
        require(state == "baseline" and why == relation == "")
        require("previous_uptime_seconds" not in raw and "previous_observed_at" not in raw)
        return out
    if parent.get("unexpected", False):
        require(state == "unavailable" and why == "unexpected_target" and "previous_observed_at" not in raw and "previous_uptime_seconds" not in raw)
    require(("previous_observed_at" in raw) == ("previous_uptime_seconds" in raw))
    if "previous_observed_at" not in raw:
        require(relation == "" and (state == "new_target" and why == "" or state == "unavailable" and why in ("previous_evidence_unavailable", "unexpected_target")))
        return out
    out["previous_uptime_seconds"] = number(raw["previous_uptime_seconds"])
    out["previous_observed_at"] = fresh_time(raw["previous_observed_at"], comparison_at, boundary)
    expected, expected_reason = compare(out["uptime_seconds"], out["observed_at"], out["previous_uptime_seconds"], out["previous_observed_at"])
    require(state == expected and why == expected_reason)
    require(relation in ("same", "different", "unknown") if state == "changed" else relation == "")
    return out


def project(raw, evidence, raw_items, generated, boundary):
    from post_deployment_builds import require, integer, timestamp, SAMPLES_MAX, NODES_MAX, CLOCK_SKEW_SECONDS
    require(isinstance(raw, dict) and isinstance(raw.get("counts"), dict))
    counts = {name: integer(raw["counts"].get(name), len(evidence["jobs"])*(SAMPLES_MAX+NODES_MAX)) for name in COUNTS}
    total = sum(counts[name] for name in COUNTS[:5])
    require(total == len(evidence["items"]) + evidence["omitted_items"])
    require(counts["compared"] == counts["changed"]+counts["unchanged"])
    require(counts["changed"] == sum(counts[name] for name in ("same_revision_changes", "different_revision_changes", "unknown_revision_changes")))
    comparison_at = None
    if "comparison_at" in raw:
        comparison_at = timestamp(raw["comparison_at"])
        compared_ns, generated_ns = sample_ns(raw["comparison_at"]), sample_ns(evidence["generated_at"])
        require(generated_ns-LOOKBACK_SECONDS*1000000000 <= compared_ns < generated_ns)
        require(compared_ns >= sample_ns(evidence["evidence_after"])-CLOCK_SKEW_SECONDS*1000000000)
        require(counts["fresh"] == sum(counts[name] for name in ("compared", "new_targets", "repeated_samples", "comparison_unavailable")))
    else:
        require(not any(counts[name] for name in ("compared", "new_targets", "removed_targets", "repeated_samples", "comparison_unavailable")))
    incomplete = any(counts[name] for name in ("missing", "stale", "conflicting", "unavailable", "new_targets", "removed_targets", "repeated_samples", "comparison_unavailable")) or evidence["counts"]["unexpected"] or any(job["state"] != "available" for job in evidence["jobs"])
    require(type(raw.get("incomplete")) is bool and (not incomplete or raw["incomplete"]))
    if counts["changed"]:
        state, reason = "changed", "observed_process_change"
    elif raw["incomplete"]:
        state, reason = "incomplete", "incomplete_continuity_evidence"
    elif comparison_at is None and counts["fresh"]:
        state, reason = "baseline", "awaiting_comparison"
    elif counts["compared"]:
        state, reason = "unchanged", ""
    else:
        state, reason = "unknown", "process_evidence_unavailable"
    require(raw.get("state") == state and raw.get("reason", "") == reason)
    out = {"state": state, "counts": counts, "incomplete": raw["incomplete"]}
    if "reason" in raw:
        out["reason"] = reason
    if comparison_at is not None:
        out["comparison_at"] = raw["comparison_at"]
    preview = [project_item(item.get("process"), parent, generated, boundary, comparison_at)
               for parent, item in zip(evidence["items"], raw_items)]
    for name in COUNTS[:5]:
        require(sum(item["state"] == name for item in preview) <= counts[name])
    for name, key in (("changed", "changed"), ("unchanged", "unchanged"), ("new_target", "new_targets"),
                      ("repeated_sample", "repeated_samples"), ("unavailable", "comparison_unavailable")):
        require(sum(item.get("comparison") == name for item in preview) <= counts[key])
    for relation in ("same", "different", "unknown"):
        require(sum(item.get("revision_relation") == relation for item in preview) <= counts[relation+"_revision_changes"])
    return out, preview


def semantic(process):
    if process is None:
        return None
    out = dict(process)
    if not sum(process["counts"][key] for key in ("changed", "new_targets", "removed_targets")):
        out.pop("comparison_at", None)
    return out


def project_progress(raw, progress, times, current, events, role):
    from post_deployment_builds import require, integer, timestamp, JOBS, SAMPLES_MAX, NODES_MAX
    require(isinstance(raw, dict))
    maximum = min((1 << 63)-1, progress["observations"]*len(JOBS[role])*(SAMPLES_MAX+NODES_MAX))
    out = {name: integer(raw.get(name), maximum) for name in PROGRESS}
    require(out["polls_with_comparison"] <= progress["observations"])
    require((out["polls_with_comparison"] == 0) == (out["compared_pairs"] == 0))
    require(out["polls_with_comparison"] <= out["compared_pairs"] and out["observed_changes"] <= out["compared_pairs"])
    require(out["observed_changes"] == sum(out[name] for name in ("same_revision_changes", "different_revision_changes", "unknown_revision_changes")))
    require(type(raw.get("observed_incomplete_evidence")) is bool)
    out["observed_incomplete_evidence"] = raw["observed_incomplete_evidence"]
    require(("first_change_at" in raw) == ("last_change_at" in raw) == (out["observed_changes"] > 0))
    if out["observed_changes"]:
        first, last = timestamp(raw["first_change_at"]), timestamp(raw["last_change_at"])
        require(times["first_observed_at"] <= first <= last <= times["last_observed_at"])
        out.update(first_change_at=raw["first_change_at"], last_change_at=raw["last_change_at"])
    process = current.get("evidence", {}).get("process")
    require(process is not None and not process["incomplete"] or out["observed_incomplete_evidence"] or progress["observations"] == 0)
    if progress["observations"] == 0:
        require(not any(out.values()))
    for p in [process] + [event.get("process") for event in events]:
        require(p is not None and not p["incomplete"] or out["observed_incomplete_evidence"] or progress["observations"] == 0)
        if p:
            for source, target in (("changed", "observed_changes"), ("compared", "compared_pairs"),
                                   ("same_revision_changes", "same_revision_changes"), ("different_revision_changes", "different_revision_changes"),
                                   ("unknown_revision_changes", "unknown_revision_changes"), ("new_targets", "new_targets"), ("removed_targets", "removed_targets")):
                require(p["counts"][source] <= out[target])
    if events:
        require(semantic(events[-1].get("process")) == semantic(process))
    return out


def summary(evidence, progress=None):
    process = evidence.get("process")
    text = "\nRuntime process continuity (advisory): **" + (process["state"] if process else "unknown") + "**.\n\n"
    if process:
        text += ", ".join(name+"="+str(process["counts"][name]) for name in COUNTS) + ".\n"
        text += "Observed changes do not prove crashes or count every restart between samples.\n"
    else:
        text += "Process evidence unavailable (" + evidence.get("process_unavailable_reason", "process_evidence_not_returned") + ").\n"
    if progress and "process" in progress:
        p = progress["process"]
        text += "\nObserved process history: " + ", ".join(name+"="+str(p[name]) for name in PROGRESS) + ".\n"
        for name in ("first_change_at", "last_change_at"):
            if name in p:
                text += "- process_"+name+": "+p[name]+"\n"
        text += "- process_observed_incomplete_evidence: "+str(p["observed_incomplete_evidence"]).lower()+"\n"
    if progress and "process_unavailable_reason" in progress:
        text += "\nProcess history unavailable ("+progress["process_unavailable_reason"]+").\n"
    return text
