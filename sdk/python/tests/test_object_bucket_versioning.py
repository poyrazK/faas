"""Versioning configuration preserves provider truth and durable progress."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import get_object_bucket_versioning, put_object_bucket_versioning
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectBucketVersioning, ObjectBucketVersioningRequest


def test_bucket_versioning_request_and_progress() -> None:
    bucket = UUID("00000000-0000-0000-0000-000000000001")
    calls = []

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request.method)
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/versioning"
        if request.method == "PUT":
            assert json.loads(request.content) == {"status": "Enabled"}
        return httpx.Response(
            202 if request.method == "PUT" else 200,
            json={
                "bucket_id": str(bucket),
                "desired_status": "Enabled",
                "observed_status": "Enabled",
                "state": "propagating",
                "revision": 1,
                "versions_required": True,
                "updated_at": "2026-10-02T00:00:00Z",
            },
        )

    with httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.example.test", token="token").set_httpx_client(transport)
        body = ObjectBucketVersioningRequest(status="Enabled")
        created = put_object_bucket_versioning.sync("demo", bucket, client=client, body=body)
        read = get_object_bucket_versioning.sync("demo", bucket, client=client)
        assert isinstance(created, ObjectBucketVersioning)
        assert isinstance(read, ObjectBucketVersioning)
        assert read.to_dict()["observed_status"] == "Enabled"
        assert read.to_dict()["state"] == "propagating"
        assert read.versions_required is True
        assert calls == ["PUT", "GET"]
