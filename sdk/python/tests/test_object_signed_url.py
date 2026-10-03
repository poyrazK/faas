"""Branded object URLs preserve owned encryption and durable write receipts."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import sign_bucket_object
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectEncryption, ObjectSignedRequest, ObjectSignRequest


def test_signed_object_request_and_receipt() -> None:
    bucket = UUID("11111111-1111-4111-8111-111111111111")
    receipt = UUID("33333333-3333-4333-8333-333333333333")
    key = "arn:gregale:kms:us-east-1:11111111-1111-4111-8111-111111111111:key/22222222-2222-4222-8222-222222222222"

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/signed-url"
        assert request.headers["Authorization"] == "Bearer token"
        assert json.loads(request.content) == {
            "method": "PUT",
            "key": "file",
            "size_bytes": 3,
            "expires_in": 300,
            "encryption": {"algorithm": "aws:kms", "key_id": key, "bucket_key_enabled": False},
        }
        return httpx.Response(
            200,
            json={
                "url": "https://s3.gregale.dev/assets/file",
                "method": "PUT",
                "headers": {},
                "expires_at": "2026-10-04T00:00:00Z",
                "upload_id": str(receipt),
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        body = ObjectSignRequest(
            method="PUT",
            key="file",
            size_bytes=3,
            encryption=ObjectEncryption(algorithm="aws:kms", key_id=key, bucket_key_enabled=False),
        )
        out = sign_bucket_object.sync("demo", bucket, client=client, body=body)
        assert isinstance(out, ObjectSignedRequest)
        assert out.upload_id == receipt
