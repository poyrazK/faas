from faas_sdk.preview.api.apps import (
    admit_binding_verification_run_step,
    cancel_binding_verification_run,
    complete_binding_verification_run,
    create_binding_verification_run,
    get_binding_verification_run,
    resume_binding_verification_run,
)
from faas_sdk.preview.models.create_binding_verification_run_request import CreateBindingVerificationRunRequest


def test_verification_run_identity_consent_and_atomic_step_routes():
    data = {
        "id": "11111111-1111-4111-8111-111111111111",
        "deployment_id": "22222222-2222-4222-8222-222222222222",
        "review_digest": "a" * 64,
        "ttl_seconds": 60,
        "object_storage_write": False,
        "queue_publish": True,
    }
    body = CreateBindingVerificationRunRequest.from_dict(data)
    created = create_binding_verification_run._get_kwargs("app", body=body)
    assert created["json"] == data
    base = "/v1/apps/app/bindings/verification-runs/" + data["id"]
    assert get_binding_verification_run._get_kwargs("app", data["id"])["url"] == base
    assert resume_binding_verification_run._get_kwargs("app", data["id"])["url"] == base + "/resume"
    assert complete_binding_verification_run._get_kwargs("app", data["id"])["url"] == base + "/complete"
    assert cancel_binding_verification_run._get_kwargs("app", data["id"])["method"] == "delete"
    step = admit_binding_verification_run_step._get_kwargs("app", data["id"], 2)
    assert step["method"] == "post"
    assert step["url"] == base + "/steps/2/admit"


def test_app_review_selects_live_without_client_hashing():
    from faas_sdk.preview.api.apps import review_binding_verification
    from faas_sdk.preview.models.review_binding_verification_request import ReviewBindingVerificationRequest

    wire = review_binding_verification._get_kwargs("app", body=ReviewBindingVerificationRequest())
    assert wire["method"] == "post"
    assert wire["url"] == "/v1/apps/app/bindings/verification-runs/review"
    assert wire["json"] == {}


def test_app_review_response_forwards_server_selection():
    from faas_sdk.preview.models.binding_verification_review import BindingVerificationReview

    identity = "11111111-1111-4111-8111-111111111111"
    target = "22222222-2222-4222-8222-222222222222"
    review = BindingVerificationReview.from_dict(
        {
            "app": "app",
            "deployment_id": target,
            "scope": "production",
            "service_targets": {"billing": target},
            "reviewed_at": "2026-10-14T00:00:00Z",
            "ready": True,
            "review_digest": "opaque-server-digest",
            "object_storage_write_required": False,
            "queue_publish_required": False,
            "plan": [],
            "blockers": [],
            "inventory": {"app": "app", "generated_at": "2026-10-14T00:00:00Z", "complete": True, "bindings": []},
            "policy": {
                "app_id": identity,
                "scope": "production",
                "mode": "enforce",
                "revision": 1,
                "max_verification_age": "5m",
                "require_application_ack": False,
            },
        }
    )
    request = CreateBindingVerificationRunRequest.from_dict(
        {
            "id": identity,
            "deployment_id": str(review.deployment_id),
            "service_targets": review.service_targets.to_dict(),
            "review_digest": review.review_digest,
            "ttl_seconds": 60,
            "object_storage_write": False,
            "queue_publish": False,
        }
    )
    wire = create_binding_verification_run._get_kwargs("app", body=request)["json"]
    assert wire["review_digest"] == "opaque-server-digest"
    assert wire["service_targets"] == {"billing": target}
    assert resume_binding_verification_run._get_kwargs("app", identity)["url"].endswith(identity + "/resume")
