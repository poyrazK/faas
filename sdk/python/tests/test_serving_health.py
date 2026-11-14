"""Serving coverage must preserve unknowns and configured-empty semantics."""

from faas_sdk.preview.models.obs_serving_health import ObsServingHealth


def snapshot() -> dict:
    return {
        "generated_at": "2026-11-13T12:00:00+00:00",
        "scope": "expected_serving_probe_coverage",
        "state": "partial",
        "reason": "inventory_missing",
        "max_age_seconds": 120,
        "inventory": {
            "state": "unavailable",
            "reason": "inventory_missing",
            "configured": None,
            "expected_pairs": None,
            "observed_at": None,
        },
        "counts": None,
        "probes": [],
        "limitations": ["url_values_redacted_to_sha256"],
    }


def counts(expected: int = 0, unknown: int = 0) -> dict:
    return dict.fromkeys(
        ("reporting", "healthy", "missing", "stale", "invalid", "content_disabled", "budget_mismatch", "failing"), 0
    ) | {"expected": expected, "unknown": unknown}


def test_unknown_inventory_never_becomes_zero_expected_probes() -> None:
    body = snapshot()
    decoded = ObsServingHealth.from_dict(body)
    assert decoded.counts is None
    assert decoded.inventory.configured is None
    assert decoded.inventory.expected_pairs is None
    assert decoded.to_dict() == body


def test_configured_empty_fleet_is_not_applicable_and_keeps_zero_counts() -> None:
    body = snapshot()
    body["state"], body["reason"] = "not_applicable", "zero_expected_probes"
    body["inventory"] = {
        "state": "healthy",
        "configured": True,
        "expected_pairs": 0,
        "observed_at": body["generated_at"],
    }
    body["counts"] = counts()
    decoded = ObsServingHealth.from_dict(body)
    assert decoded.counts.expected == 0
    assert decoded.inventory.configured is True
    assert decoded.to_dict() == body


def test_unknown_recording_preserves_null_reporting_and_run_time() -> None:
    body = snapshot()
    body["reason"] = "incomplete_evidence"
    body["inventory"] = {
        "state": "healthy",
        "configured": True,
        "expected_pairs": 1,
        "observed_at": body["generated_at"],
    }
    body["counts"] = counts(1, 1)
    body["probes"] = [
        {
            "node": "fsn-2",
            "url_id": "a" * 64,
            "state": "unknown",
            "reason": "coverage_missing",
            "reporting": None,
            "evaluated_at": None,
            "last_run_at": None,
            "expected_first_byte_seconds": 2,
            "max_result_age_seconds": 900,
        }
    ]
    decoded = ObsServingHealth.from_dict(body)
    assert decoded.probes[0].reporting is None
    assert decoded.probes[0].last_run_at is None
    assert decoded.probes[0].url_id == "a" * 64
    assert decoded.to_dict() == body
