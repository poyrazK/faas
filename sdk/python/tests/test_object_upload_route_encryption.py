"""Upload route clients preserve their owned encryption policy."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import create_object_upload_route, get_object_write_receipt
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import CreateObjectUploadRouteRequest, ObjectEncryption, ObjectUploadRoute, ObjectWriteReceipt


def test_upload_route_encryption() -> None:
    bucket = UUID("11111111-1111-4111-8111-111111111111")
    encryption = {"algorithm": "aws:kms", "key_id": "owned-key", "bucket_key_enabled": False}

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.headers["Authorization"] == "Bearer token"
        if request.method == "GET":
            assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/write-receipts/{bucket}"
            return httpx.Response(
                200,
                json={
                    "id": str(bucket),
                    "bucket_id": str(bucket),
                    "key": "uploads/key",
                    "operation": "put",
                    "bytes": 3,
                    "content_type": "text/plain",
                    "etag": "verified",
                    "status": "completed",
                    "created_at": "2026-10-04T00:00:00Z",
                    "encryption": encryption,
                },
            )
        assert request.url.path == "/v1/apps/demo/upload-routes"
        assert json.loads(request.content) == {
            "name": "files",
            "bucket_id": str(bucket),
            "encryption": encryption,
            "enabled": True,
        }
        return httpx.Response(
            201,
            json={
                "id": str(bucket),
                "name": "files",
                "bucket_id": str(bucket),
                "max_bytes": 3,
                "enabled": True,
                "created_at": "2026-10-04T00:00:00Z",
                "updated_at": "2026-10-04T00:00:00Z",
                "encryption": encryption,
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        out = create_object_upload_route.sync(
            "demo",
            client=client,
            body=CreateObjectUploadRouteRequest(
                name="files",
                bucket_id=bucket,
                encryption=ObjectEncryption(algorithm="aws:kms", key_id="owned-key", bucket_key_enabled=False),
            ),
        )
        assert isinstance(out, ObjectUploadRoute)
        assert isinstance(out.encryption, ObjectEncryption)
        assert out.encryption.key_id == "owned-key"
        receipt = get_object_write_receipt.sync("demo", bucket, bucket, client=client)
        assert isinstance(receipt, ObjectWriteReceipt)
        assert isinstance(receipt.encryption, ObjectEncryption)
        assert receipt.encryption.to_dict() == encryption
