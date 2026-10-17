from io import BytesIO

from faas_sdk.api.projects import safe_apply_project
from faas_sdk.models.project_apply_request import ProjectApplyRequest
from faas_sdk.models.project_safe_release import ProjectSafeRelease
from faas_sdk.types import File


def test_atomic_additions_keep_opt_in_and_reservation_identity():
    body = ProjectApplyRequest(source=File(payload=BytesIO(b"source"), file_name="source.tar.gz"))
    kwargs = safe_apply_project._get_kwargs(
        body=body,
        deployment_attempt_id="a" * 32,
        plan_token="addition-plan",
        project_add_services="required",
        project_release_mode="atomic",
    )
    assert kwargs["headers"]["Project-Add-Services"] == "required"
    assert kwargs["headers"]["Project-Release-Mode"] == "atomic"
    raw = {
        "status": "building",
        "previous_release_id": "baseline",
        "ttl_seconds": 1800,
        "deadline": "2026-10-16T14:00:00Z",
        "add_services": True,
        "additions": [{"app_id": "metrics-app", "slug": "metrics", "root_dir": "metrics", "fingerprint": "a" * 64}],
    }
    checkpoint = ProjectSafeRelease.from_dict(raw)
    assert checkpoint.additions[0].app_id == "metrics-app"
    assert checkpoint.to_dict()["additions"] == raw["additions"]
    assert checkpoint.to_dict()["add_services"] is True
