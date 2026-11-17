"""Explicit advisory selection; core identity, readiness, process and loops stay on."""

SCOPE = "selected_runtime_diagnostics"
GROUPS = ("builder_health", "handoff_health", "build_recovery_health", "app_task_health", "command_cron_health", "command_cron_outcomes", "http_cron_delivery", "delayed_task_dispatch", "command_cron_timing", "app_task_results", "app_task_operations", "post_capture_health", "snapshot_prime_health", "preparation_health")
LEGACY = ()
ROLES = ("single-box-v1", "control-plane-v1", "single-box-v2", "control-plane-v2")


def groups(role):
    if role in ("single-box-v2", "control-plane-v2"):
        return GROUPS
    if role in ("single-box-v1", "control-plane-v1"):
        return LEGACY
    return ()


def parse(role, raw):
    from post_deployment_builds import require
    names = groups(role)
    require(role in ROLES and isinstance(raw, str) and raw and (raw in ("all", "none") or len(raw) <= len(",".join(names))))
    if raw == "all":
        wanted = names
    elif raw == "none":
        wanted = ()
    else:
        wanted = raw.split(",")
        require(len(wanted) == len(set(wanted)) and all(n in names for n in wanted))
    return {"scope": SCOPE, "selected": [n for n in names if n in wanted], "not_selected": [n for n in names if n not in wanted]}


def project(raw, role):
    from post_deployment_builds import require
    require(isinstance(raw, dict) and raw.get("scope") == SCOPE)
    names = groups(role)
    a, b = raw.get("selected"), raw.get("not_selected")
    require(role in ROLES and isinstance(a, list) and isinstance(b, list) and len(a)+len(b) == len(names) and all(isinstance(n, str) for n in a+b))
    require(a == [n for n in names if n in a] and b == [n for n in names if n not in a])
    return {"scope": SCOPE, "selected": list(a), "not_selected": list(b)}


def envelope(raw, role, expected=None, strict=False):
    from post_deployment_builds import require
    result = {}
    if expected is False:
        if "diagnostics" in raw or "diagnostics_source" in raw:
            if strict:
                require(False)
            return {"diagnostics_unavailable_reason": "unexpected_diagnostic_selection"}
        return result
    try:
        if "diagnostics" not in raw and expected is None:
            require("diagnostics_source" not in raw)
            return result
        value = project(raw.get("diagnostics"), role)
        require(expected is None or value == expected)
        source = raw.get("diagnostics_source")
        require(source in ("server", "client"))
        return {"diagnostics": value, "diagnostics_source": source}
    except (ValueError, TypeError, AttributeError):
        if strict:
            raise
        if expected is not None:
            result.update(diagnostics=project(expected, role), diagnostics_source="client")
        result["diagnostics_unavailable_reason"] = "selection_not_acknowledged" if expected is not None and "diagnostics" not in raw else "invalid_diagnostic_selection"
        return result


def selected(value, key):
    d = value.get("diagnostics")
    return d is None or key in d["selected"]


def remap(value, source_key, target_key):
    """Keep selection intact when borrowing another group's temporal validator."""
    value = dict(value)
    d = value.get("diagnostics")
    if d is not None:
        wanted = source_key in d["selected"]
        value["diagnostics"] = {**d,
            "selected": [n for n in d["selected"] if n != target_key]+([target_key] if wanted else []),
            "not_selected": [n for n in d["not_selected"] if n != target_key]+([] if wanted else [target_key])}
    return value


def query_value(value):
    return "none" if not value["selected"] else "all" if not value["not_selected"] else ",".join(value["selected"])


def summary(builds):
    d = builds.get("diagnostics")
    if d is None:
        return "\nDiagnostic selection metadata was not accepted; the default diagnostic view is retained.\n" if "diagnostics_unavailable_reason" in builds else ""
    text = "\nRuntime diagnostic selection: **"+builds["diagnostics_source"]+"**.\n\n"
    text += "Selected: "+(", ".join(d["selected"]) or "none")+".\n\n"
    text += "Not selected: "+(", ".join(d["not_selected"]) or "none")+". Intentional omissions are separate from unavailable evidence. Earlier observations stay separate from current collection availability.\n"
    if builds["diagnostics_source"] == "client":
        text += "The API did not acknowledge source-side selection; filtering was applied to the returned evidence. Reduced source query load is not established.\n"
    return text
