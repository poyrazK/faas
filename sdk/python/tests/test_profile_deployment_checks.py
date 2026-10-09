from faas_sdk.api.apps import (
    get_profile_deployment_check,
    list_profile_deployment_checks,
    save_profile_deployment_policy,
)
from faas_sdk.models.profile_deployment_check import ProfileDeploymentCheck
from faas_sdk.models.profile_deployment_policy import ProfileDeploymentPolicy
from faas_sdk.models.save_profile_deployment_policy_request import SaveProfileDeploymentPolicyRequest


def test_automatic_policy_and_pending_receipt_preserve_settings():
    config = {
        "enabled": True,
        "runtime": "node24",
        "window_seconds": 300,
        "warmup_seconds": 120,
        "options": {
            "relative_increase_percent": 20,
            "absolute_increase_cpu_per_second": 0.01,
            "minimum_profiles": 3,
            "minimum_coverage_ratio": 0.8,
        },
    }
    request = SaveProfileDeploymentPolicyRequest.from_dict({"expected_revision": 0, "config": config})
    assert request.to_dict() == {"expected_revision": 0, "config": config}
    kwargs = save_profile_deployment_policy._get_kwargs("demo", body=request)
    assert kwargs["method"] == "put"
    assert kwargs["json"]["expected_revision"] == 0
    initial = ProfileDeploymentPolicy.from_dict(
        {"app_id": "44444444-4444-4444-8444-444444444444", "revision": 0, "config": config}
    )
    assert "updated_at" not in initial.to_dict()
    at = "2026-10-08T12:00:00.123Z"
    receipt = ProfileDeploymentCheck.from_dict(
        {
            "deployment_id": "11111111-1111-4111-8111-111111111111",
            "app_id": "44444444-4444-4444-8444-444444444444",
            "scope": "prod",
            "policy_revision": 1,
            "config": config,
            "candidate": {
                "deployment_id": "11111111-1111-4111-8111-111111111111",
                "runtime": "node24",
                "start": at,
                "end": "2026-10-08T12:05:00.123Z",
            },
            "status": "queued",
            "reason": "Waiting for capture",
            "attempts": 0,
            "next_attempt_at": at,
            "created_at": at,
        }
    )
    assert receipt.candidate.start.microsecond == 123000
    assert receipt.status == "queued"
    assert "baseline" not in receipt.to_dict()
    assert "completed_at" not in receipt.to_dict()
    assert list_profile_deployment_checks._get_kwargs("demo")["url"].endswith("/deployment-checks")
    assert get_profile_deployment_check._get_kwargs("demo", "dep")["url"].endswith("/deployment-checks/dep")
