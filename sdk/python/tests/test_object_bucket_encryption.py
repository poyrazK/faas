"""Owned bucket defaults preserve selections and durable progress."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import (
    delete_object_bucket_encryption,
    get_object_bucket_encryption,
    put_object_bucket_encryption,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectBucketEncryption, ObjectBucketEncryptionRequest, ObjectEncryption


def test_bucket_default_encryption_configuration() -> None:
    bucket = UUID("00000000-0000-0000-0000-000000000001")
    calls = []

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request.method)
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/encryption"
        assert request.headers["Authorization"] == "Bearer token"
        if request.method == "PUT":
            assert json.loads(request.content) == {"encryption": {"algorithm": "AES256"}}
        return httpx.Response(
            200 if request.method == "GET" else 202,
            json={
                "bucket_id": str(bucket),
                "state": "waiting",
                "revision": 2,
                "desired_encryption": {"algorithm": "AES256"},
                "updated_at": "2026-10-04T00:00:00Z",
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        body = ObjectBucketEncryptionRequest(encryption=ObjectEncryption(algorithm="AES256"))
        created = put_object_bucket_encryption.sync("demo", bucket, client=client, body=body)
        read = get_object_bucket_encryption.sync("demo", bucket, client=client)
        cleared = delete_object_bucket_encryption.sync("demo", bucket, client=client)
        assert isinstance(created, ObjectBucketEncryption)
        assert isinstance(read, ObjectBucketEncryption)
        assert isinstance(cleared, ObjectBucketEncryption)
        assert created.to_dict()["desired_encryption"] == {"algorithm": "AES256"}
        assert read.to_dict()["state"] == "waiting"
        assert calls == ["PUT", "GET", "DELETE"]
