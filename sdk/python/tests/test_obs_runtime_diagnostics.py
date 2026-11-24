import datetime as dt

import pytest
from faas_sdk.preview.api.admin import get_obs_runtime_builds
from faas_sdk.preview.models import ObsRuntimeBuilds, ObsRuntimeDiagnostics

GROUPS = (
    "builder_health",
    "handoff_health",
    "build_recovery_health",
    "app_task_health",
    "command_cron_health",
    "command_cron_outcomes",
    "http_cron_delivery",
    "delayed_task_dispatch",
    "command_cron_timing",
    "app_task_results",
    "app_task_operations",
    "post_capture_health",
    "snapshot_prime_health",
    "preparation_health",
)


@pytest.mark.parametrize(
    "role,selected",
    (
        ("single-box-v2", ()),
        ("single-box-v2", GROUPS),
        (
            "single-box-v2",
            (
                "http_cron_delivery",
                "delayed_task_dispatch",
            ),
        ),
        ("single-box-v2", ("builder_health", "http_cron_delivery")),
        ("single-box-v1", ()),
        ("control-plane-v1", ()),
    ),
)
def test_runtime_diagnostic_selection_roundtrip_and_query(role, selected):
    names = GROUPS if role.endswith("-v2") else ()
    wanted = {
        "scope": "selected_runtime_diagnostics",
        "selected": list(selected),
        "not_selected": [n for n in names if n not in selected],
    }
    assert ObsRuntimeDiagnostics.from_dict(wanted).to_dict() == wanted
    now = "2026-11-17T01:00:00+00:00"
    payload = {
        "generated_at": now,
        "scope": "selected_critical_daemon_builds",
        "expected_role": role,
        "requested_release": "a" * 40,
        "evidence_after": "2026-11-17T00:59:00+00:00",
        "state": "unknown",
        "reason": "identity_unavailable",
        "max_age_seconds": 120,
        "counts": dict.fromkeys(
            (
                "expected",
                "observed",
                "matching",
                "mismatched",
                "missing",
                "stale",
                "unavailable",
                "unexpected",
                "distinct_revisions",
            ),
            0,
        ),
        "jobs": [],
        "items": [],
        "omitted_items": 0,
        "limitations": [],
        "diagnostics": wanted,
    }
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.diagnostics, ObsRuntimeDiagnostics)
    assert parsed.to_dict() == payload
    raw = ",".join(selected) if selected else "none"
    kwargs = get_obs_runtime_builds._get_kwargs(release="a" * 40, after=dt.datetime.fromisoformat(now), diagnostics=raw)
    assert kwargs["params"]["diagnostics"] == raw
    default = get_obs_runtime_builds._get_kwargs(release="a" * 40, after=dt.datetime.fromisoformat(now))
    assert "diagnostics" not in default["params"]
