"""Branded object URLs preserve owned encryption and durable write receipts."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import create_object_multipart_upload, sign_bucket_object
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import (
    CreateObjectMultipartUploadRequest,
    ObjectEncryption,
    ObjectMultipartUpload,
    ObjectSignedRequest,
    ObjectSignRequest,
)


def test_historical_read_and_public_version_pagination() -> None:
    from faas_sdk.api.storage import list_object_bucket_versions
    from faas_sdk.models import ObjectVersionList

    bucket = UUID("11111111-1111-4111-8111-111111111111")
    version = UUID("22222222-2222-4222-8222-222222222222")
    key = "目录 /+%.txt"

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.headers["Authorization"] == "Bearer token"
        if request.method == "POST":
            assert json.loads(request.content) == {
                "method": "GET",
                "key": key,
                "expires_in": 300,
                "version_id": str(version),
            }
            return httpx.Response(
                200,
                json={
                    "url": "https://s3.gregale.dev/assets/file",
                    "method": "GET",
                    "headers": {},
                    "expires_at": "2026-10-07T00:00:00Z",
                },
            )
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/objects/versions"
        assert request.url.params["key_marker"] == key
        assert request.url.params["version_id_marker"] == str(version)
        assert request.url.params["prefix"] == "目录"
        assert request.url.params["limit"] == "1"
        return httpx.Response(
            200,
            json={
                "items": [
                    {
                        "key": key,
                        "version_id": str(version),
                        "is_latest": False,
                        "delete_marker": False,
                        "size_bytes": 3,
                        "last_modified": "2026-10-07T00:00:00Z",
                    }
                ],
                "common_prefixes": [],
                "next_key_marker": key,
                "next_version_id_marker": str(version),
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        sign_bucket_object.sync(
            "demo", bucket, client=client, body=ObjectSignRequest(method="GET", key=key, version_id=version)
        )
        page = list_object_bucket_versions.sync(
            "demo", bucket, client=client, prefix="目录", key_marker=key, version_id_marker=str(version), limit=1
        )
        assert isinstance(page, ObjectVersionList)
        assert page.items[0].version_id == str(version)
        assert page.next_key_marker == key
        assert page.next_version_id_marker == str(version)


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


def test_control_multipart_encryption() -> None:
    bucket = UUID("11111111-1111-4111-8111-111111111111")
    encryption = {"algorithm": "aws:kms", "key_id": "owned-key", "bucket_key_enabled": False}

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/multipart-uploads"
        assert json.loads(request.content) == {"key": "file", "size_bytes": 3, "encryption": encryption}
        return httpx.Response(
            201,
            json={
                "id": str(bucket),
                "key": "file",
                "size_bytes": 3,
                "part_size_bytes": 3,
                "part_count": 1,
                "content_type": "application/octet-stream",
                "state": "active",
                "expires_at": "2026-10-04T00:00:00Z",
                "created_at": "2026-10-03T23:00:00Z",
                "encryption": encryption,
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        body = CreateObjectMultipartUploadRequest(
            key="file",
            size_bytes=3,
            encryption=ObjectEncryption(algorithm="aws:kms", key_id="owned-key", bucket_key_enabled=False),
        )
        out = create_object_multipart_upload.sync("demo", bucket, client=client, body=body)
        assert isinstance(out, ObjectMultipartUpload)
        assert isinstance(out.encryption, ObjectEncryption)
        assert out.encryption.key_id == "owned-key"
