from faas_sdk.models.profile_investigation_response import ProfileInvestigationResponse
from faas_sdk.models.save_profile_investigation_request import SaveProfileInvestigationRequest


def test_saved_investigation_keeps_expired_notes_and_complete_path():
    query = {
        "deployment_id": "11111111-1111-4111-8111-111111111111",
        "runtime": "node24",
        "start": "2026-10-07T11:00:00.123Z",
        "end": "2026-10-07T12:00:00.123Z",
    }
    payload = {
        "saved": {
            "id": "33333333-3333-4333-8333-333333333333",
            "app_id": "44444444-4444-4444-8444-444444444444",
            "revision": 3,
            "created_at": query["end"],
            "updated_at": query["end"],
            "investigation": {
                "title": "Regression",
                "findings": "parseJSON",
                "notes": "Saved notes",
                "baseline": query,
                "candidate": query,
                "selected_path": {
                    "view": "comparison",
                    "frames": [{"name": "all"}, {"name": "parseJSON", "file": "app.js", "line": 42}],
                },
            },
        },
        "url": "/dashboard/apps/demo/profiles?investigation_id=33333333-3333-4333-8333-333333333333",
        "baseline_status": {"status": "expired", "detail": "Notes remain available"},
        "candidate_status": {"status": "retained", "detail": "Samples may be absent"},
    }
    parsed = ProfileInvestigationResponse.from_dict(payload)
    assert parsed.baseline_status.status == "expired"
    assert parsed.saved.investigation.notes == "Saved notes"
    encoded = parsed.to_dict()
    assert encoded["saved"]["investigation"]["selected_path"] == payload["saved"]["investigation"]["selected_path"]
    assert parsed.saved.investigation.candidate.end.microsecond == 123000
    req = SaveProfileInvestigationRequest.from_dict(
        {"expected_revision": 0, "investigation": payload["saved"]["investigation"]}
    )
    assert req.to_dict()["expected_revision"] == 0
    req.expected_revision = parsed.saved.revision
    assert req.to_dict()["expected_revision"] == 3


def test_unselected_call_path_stays_absent():
    query = {
        "deployment_id": "11111111-1111-4111-8111-111111111111",
        "runtime": "node24",
        "start": "2026-10-07T11:00:00Z",
        "end": "2026-10-07T12:00:00Z",
    }
    req = SaveProfileInvestigationRequest.from_dict(
        {
            "expected_revision": 0,
            "investigation": {
                "title": "Regression",
                "findings": "",
                "notes": "",
                "baseline": query,
                "candidate": query,
            },
        }
    )
    assert "selected_path" not in req.to_dict()["investigation"]


def test_regression_check_defaults_and_historical_assessment():
    from uuid import UUID

    from faas_sdk.api.apps.check_profile_regression import _get_kwargs
    from faas_sdk.models.check_profile_regression_request import CheckProfileRegressionRequest
    from faas_sdk.models.profile_regression_assessment import ProfileRegressionAssessment

    request = CheckProfileRegressionRequest(expected_revision=3)
    kwargs = _get_kwargs("demo", UUID("33333333-3333-4333-8333-333333333333"), body=request)
    assert kwargs["method"] == "post"
    assert kwargs["url"].endswith("/investigations/33333333-3333-4333-8333-333333333333/check")
    assert kwargs["json"] == {"expected_revision": 3}
    query = {
        "deployment_id": "11111111-1111-4111-8111-111111111111",
        "runtime": "node24",
        "start": "2026-10-07T11:00:00Z",
        "end": "2026-10-07T12:00:00Z",
    }
    assessment = ProfileRegressionAssessment.from_dict(
        {
            "investigation_revision": 4,
            "checked_at": query["end"],
            "status": "inconclusive",
            "reason": "History expired",
            "options": {
                "relative_increase_percent": 20,
                "absolute_increase_cpu_per_second": 0.01,
                "minimum_profiles": 3,
                "minimum_coverage_ratio": 0.8,
            },
            "baseline": query,
            "candidate": query,
            "evidence": [],
            "uncomparable_entries": 0,
        }
    )
    assert assessment.status == "inconclusive"
    assert assessment.options.minimum_coverage_ratio == 0.8
    encoded = assessment.to_dict()
    assert "total" not in encoded
    assert encoded["investigation_revision"] == 4
    assert encoded["evidence"] == []
