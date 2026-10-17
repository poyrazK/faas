from faas_sdk.api.projects import (
    cancel_project_binding_verification_run,
    complete_project_binding_verification_run,
    create_project_binding_verification_run,
    get_project_binding_verification_run,
    resume_project_binding_verification_run,
    review_project_binding_verification,
)
from faas_sdk.models.create_project_binding_verification_run_request import CreateProjectBindingVerificationRunRequest


def test_graph_run_frozen_review_and_routes():
    identity = "11111111-1111-4111-8111-111111111111"
    target = "22222222-2222-4222-8222-222222222222"
    data = {
        "id": identity,
        "ttl_seconds": 60,
        "object_storage_write": False,
        "queue_publish": True,
        "review_digests": {"app": "a" * 64},
        "review_digest": "b" * 64,
        "manifest": {
            "project": "project",
            "environment": "production",
            "ttl_seconds": 60,
            "deployments": {"app": target},
            "expected_active_release_id": "",
            "expected_fallback_deployments": {},
            "expected_policy_revisions": {"app": 3},
            "service_targets": {"app": {"billing": target}},
        },
    }
    body = CreateProjectBindingVerificationRunRequest.from_dict(data)
    assert create_project_binding_verification_run._get_kwargs("project", "production", body=body)["json"] == data
    base = "/v1/projects/project/environments/production/release-sets/verification-runs/" + identity
    assert get_project_binding_verification_run._get_kwargs("project", "production", identity)["url"] == base
    assert (
        resume_project_binding_verification_run._get_kwargs("project", "production", identity)["url"]
        == base + "/resume"
    )
    assert (
        complete_project_binding_verification_run._get_kwargs("project", "production", identity)["url"]
        == base + "/complete"
    )
    assert cancel_project_binding_verification_run._get_kwargs("project", "production", identity)["method"] == "delete"


def test_graph_review_request_preserves_consent_and_manifest():
    from faas_sdk.models.review_project_binding_verification_request import ReviewProjectBindingVerificationRequest

    manifest = {
        "project": "project",
        "environment": "production",
        "ttl_seconds": 60,
        "deployments": {},
        "expected_active_release_id": "",
        "expected_fallback_deployments": {},
        "expected_policy_revisions": {},
        "service_targets": {},
    }
    data = {"manifest": manifest, "object_storage_write": False, "queue_publish": True}
    request = ReviewProjectBindingVerificationRequest.from_dict(data)
    wire = review_project_binding_verification._get_kwargs("project", "production", body=request)
    assert wire["url"].endswith("/verification-runs/review")
    assert wire["method"] == "post"
    assert wire["json"] == data


def test_graph_review_response_forwards_aggregate_and_member_digests():
    from faas_sdk.models.project_binding_verification_review import ProjectBindingVerificationReview

    identity = "11111111-1111-4111-8111-111111111111"
    manifest = {
        "project": "project",
        "environment": "production",
        "ttl_seconds": 60,
        "deployments": {},
        "expected_active_release_id": "",
        "expected_fallback_deployments": {},
        "expected_policy_revisions": {},
        "service_targets": {},
    }
    review = ProjectBindingVerificationReview.from_dict(
        {
            "project": "project",
            "environment": "production",
            "reviewed_at": "2026-10-14T00:00:00Z",
            "ready": True,
            "review_digest": "opaque-aggregate",
            "review_digests": {},
            "manifest": manifest,
            "members": {},
            "blockers": [],
            "object_storage_write": False,
            "queue_publish": True,
            "object_storage_write_required": False,
            "queue_publish_required": True,
        }
    )
    request = CreateProjectBindingVerificationRunRequest.from_dict(
        {
            "id": identity,
            "manifest": review.manifest.to_dict(),
            "review_digest": review.review_digest,
            "review_digests": review.review_digests.to_dict(),
            "ttl_seconds": 60,
            "object_storage_write": review.object_storage_write,
            "queue_publish": review.queue_publish,
        }
    )
    wire = create_project_binding_verification_run._get_kwargs("project", "production", body=request)["json"]
    assert wire["review_digest"] == "opaque-aggregate"
    assert wire["queue_publish"] is True
    assert resume_project_binding_verification_run._get_kwargs("project", "production", identity)["url"].endswith(
        identity + "/resume"
    )
