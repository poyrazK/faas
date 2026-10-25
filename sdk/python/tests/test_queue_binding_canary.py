from uuid import UUID

from faas_sdk.preview.api.apps import cancel_queue_binding_canary, create_queue_binding_canary
from faas_sdk.preview.models.create_queue_binding_canary_request import CreateQueueBindingCanaryRequest
from faas_sdk.preview.models.queue_binding_canary import QueueBindingCanary


def test_queue_canary_preserves_exact_publication_and_completion_identity():
    data = {
        "deployment_id": "11111111-1111-4111-8111-111111111111",
        "binding": "orders",
        "ttl_seconds": 60,
        "publish_consent": True,
    }
    body = CreateQueueBindingCanaryRequest.from_dict(data)
    request = create_queue_binding_canary._get_kwargs("api", body=body)
    assert request["url"] == "/v1/apps/api/bindings/queue-canaries"
    assert request["json"] == data
    report = {
        "id": "22222222-2222-4222-8222-222222222222",
        "binding": "orders",
        "deployment_id": data["deployment_id"],
        "scope": "production",
        "release_id": "33333333-3333-4333-8333-333333333333",
        "status": "published",
        "published_at": "2026-10-14T00:00:00+00:00",
        "expires_at": "2026-10-14T00:01:00+00:00",
    }
    receipt = QueueBindingCanary.from_dict(report)
    assert receipt.release_id == UUID(report["release_id"])
    assert receipt.to_dict() == report
    cancel = cancel_queue_binding_canary._get_kwargs("api", report["id"])
    assert cancel["method"] == "delete"
    assert cancel["url"].endswith(report["id"])
