"""Serving coverage must preserve unknowns and configured-empty semantics."""

from faas_sdk.preview.models.obs_serving_health import ObsServingHealth
from faas_sdk.preview.models.obs_serving_probe import ObsServingProbe
from faas_sdk.types import UNSET


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


def probe() -> dict:
    return {
        "node": "fsn-2", "url_id": "a" * 64, "state": "healthy", "reporting": True,
        "evaluated_at": "2026-11-13T12:00:00+00:00", "last_run_at": "2026-11-13T11:59:00+00:00",
        "expected_first_byte_seconds": 2, "max_result_age_seconds": 900,
    }


def test_older_probe_without_diagnostics_remains_supported() -> None:
    body = probe()
    decoded = ObsServingProbe.from_dict(body)
    assert decoded.diagnostics is UNSET
    assert decoded.to_dict() == body


def test_unknown_diagnostics_preserve_missing_values() -> None:
    body = probe()
    body["diagnostics"] = {
        "state": "unknown", "reason": "diagnostics_missing", "evaluated_at": None,
        "attempts": None, "http_status": None, "recovered_on_retry": None,
    }
    decoded = ObsServingProbe.from_dict(body)
    assert decoded.diagnostics.attempts is None
    assert decoded.diagnostics.recovered_on_retry is None
    assert decoded.diagnostics.failure_code is UNSET
    assert decoded.to_dict() == body


def test_retry_recovery_retains_the_first_failure() -> None:
    body = probe()
    body["diagnostics"] = {
        "state": "available", "evaluated_at": body["evaluated_at"], "failure_code": "none",
        "first_failure_code": "tls", "attempts": 2, "http_status": 200, "recovered_on_retry": True,
    }
    decoded = ObsServingProbe.from_dict(body)
    assert decoded.diagnostics.first_failure_code == "tls"
    assert decoded.diagnostics.failure_code == "none"
    assert decoded.diagnostics.recovered_on_retry is True
    assert decoded.to_dict() == body


def test_older_server_without_probe_notifications_remains_supported() -> None:
    body = snapshot()
    decoded = ObsServingHealth.from_dict(body)
    assert decoded.probe_notifications is UNSET
    assert decoded.to_dict() == body


def test_unknown_probe_notification_history_preserves_nulls() -> None:
    body = snapshot()
    body["probe_notifications"] = {
        "state": "unknown", "reason": "incomplete_evidence", "expected_channels": 2,
        "counts": {"disabled": 0, "unknown": 2, "last_submission_succeeded": 0, "last_submission_failed": 0},
        "limit": 32, "omitted": 0, "truncated": False,
        "channels": [{"node": "fsn-2", "channel": channel, "state": "unknown", "reason": "recording_unknown",
                      "enabled": None, "evaluated_at": body["generated_at"], "last_run_at": None,
                      "last_attempt_at": None, "last_success_at": None} for channel in ("heartbeat", "webhook")],
    }
    decoded = ObsServingHealth.from_dict(body)
    assert decoded.probe_notifications.counts.unknown == 2
    assert decoded.probe_notifications.channels[0].enabled is None
    assert decoded.probe_notifications.channels[0].last_success_at is None
    assert decoded.probe_notifications.channels[0].failure_code is UNSET
    assert decoded.probe_notifications.channels[0].evidence_reason is UNSET
    assert decoded.to_dict() == body


def test_probe_notification_failure_retains_historical_success_and_truncation() -> None:
    body = snapshot()
    body["probe_notifications"] = {
        "state": "available", "expected_channels": 2000,
        "counts": {"disabled": 0, "unknown": 0, "last_submission_succeeded": 0, "last_submission_failed": 2000},
        "limit": 32, "omitted": 1968, "truncated": True,
        "channels": [{"node": f"node-{i:04}", "channel": "webhook", "state": "last_submission_failed",
                      "enabled": True, "evaluated_at": body["generated_at"], "last_run_at": body["generated_at"],
                      "last_attempt_at": "2026-11-12T12:00:00+00:00", "last_success_at": "2026-11-11T12:00:00+00:00",
                      "failure_code": "timeout"} for i in range(32)],
    }
    decoded = ObsServingHealth.from_dict(body)
    assert decoded.probe_notifications.counts.last_submission_failed == 2000
    assert decoded.probe_notifications.omitted == 1968
    assert decoded.probe_notifications.channels[0].last_success_at.isoformat() == "2026-11-11T12:00:00+00:00"
    assert decoded.to_dict() == body
