import json
from uuid import UUID

import httpx

from faas_sdk.preview.api.runs import deliver_run_artifact, get_run_artifact_delivery, sign_run_artifact_delivery
from faas_sdk.preview.client import AuthenticatedClient
from faas_sdk.preview.models.deliver_run_artifact_request import DeliverRunArtifactRequest


def test_artifact_delivery_replays_storage_identity_and_reads_destination_receipt():
    requests = []
    delivery_id = UUID("12345678-1234-1234-1234-123456789abc")

    def handle(request):
        requests.append(request)
        if request.url.path.endswith("/download-url"):
            return httpx.Response(200, json={"url": "https://objects.example.test/report", "method": "GET", "headers": {}, "expires_at": "2026-11-17T16:00:00Z"})
        return httpx.Response(202, json={"id": str(delivery_id), "status": "pending", "size_bytes": 20, "content_type": "text/html", "receipt_url": "/receipt", "created_at": "2026-11-17T15:00:00Z"})

    client = AuthenticatedClient(base_url="https://api.example.test", token="test-key").set_httpx_client(
        httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handle)))
    body = DeliverRunArtifactRequest(idempotency_key="completion-one", execution_id=delivery_id,
                                    artifact_name="report.html", size_bytes=20, sha256="sha256:expected", content_type="text/html")
    first = deliver_run_artifact.sync("app", "reports", client=client, body=body)
    replay = deliver_run_artifact.sync("app", "reports", client=client, body=body)
    assert first.id == replay.id == delivery_id
    assert json.loads(requests[0].content) == json.loads(requests[1].content) == body.to_dict()
    get_run_artifact_delivery.sync("app", "reports", delivery_id, client=client)
    assert sign_run_artifact_delivery.sync("app", "reports", delivery_id, client=client).method == "GET"
    assert [r.url.path for r in requests] == [
        "/v1/apps/app/run-artifact-deliveries/reports",
        "/v1/apps/app/run-artifact-deliveries/reports",
        f"/v1/apps/app/run-artifact-deliveries/reports/{delivery_id}",
        f"/v1/apps/app/run-artifact-deliveries/reports/{delivery_id}/download-url",
    ]
