from faas_sdk.preview.models.project_deployment_check_request import ProjectDeploymentCheckRequest
from faas_sdk.preview.models.project_deployment_readiness import ProjectDeploymentReadiness


def test_deployment_readiness_preserves_unknown_and_quota_evidence():
    raw = {
        "schema_version": 1,
        "project_slug": "shop",
        "environment": "production",
        "status": "blocked",
        "checked_at": "2026-10-16T12:00:00+00:00",
        "runtime_checked": False,
        "checks": [
            {
                "code": "worker_replica_overlap",
                "status": "blocked",
                "message": "Minimum replica overlap",
                "remedy": "Use sufficient replica quota",
                "observed": 6,
                "limit": 3,
            },
            {
                "code": "runtime_inputs_unavailable",
                "status": "unknown",
                "workload": "api",
                "message": "Inputs unavailable",
            },
        ],
    }
    assert ProjectDeploymentReadiness.from_dict(raw).to_dict() == raw
    request = ProjectDeploymentCheckRequest.from_dict(
        {"from_environment": "staging", "verify": True, "sync_config": True}
    )
    encoded = request.to_dict()
    assert "environment" not in encoded  # The server applies the production default.
    assert encoded["from_environment"] == "staging"
    assert encoded["verify"] is True and encoded["sync_config"] is True
    assert "workloads" not in encoded
