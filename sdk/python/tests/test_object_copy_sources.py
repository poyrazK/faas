"""Copy-only source authority stays typed and carries no private grant epoch."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import delete_object_s3_copy_source, list_object_s3_copy_sources, set_object_s3_copy_source
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectS3CopySource, ObjectS3CopySourceList, SetObjectS3CopySourceRequest


def test_copy_source_grants() -> None:
    bucket = UUID("00000000-0000-4000-8000-000000000001")
    credential = UUID("00000000-0000-4000-8000-000000000002")
    source = UUID("00000000-0000-4000-8000-000000000003")
    grant = {
        "source_bucket_id": str(source),
        "prefix": "allowed/",
        "created_at": "2026-10-04T00:00:00Z",
        "updated_at": "2026-10-04T00:00:00Z",
    }
    calls = []

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request.method)
        base = f"/v1/apps/demo%2Fx/buckets/{bucket}/s3-credentials/{credential}/copy-sources"
        expected = base + ("" if request.method == "GET" else f"/{source}")
        assert request.url.raw_path.decode() == expected
        assert request.headers["Authorization"] == "Bearer token"
        if request.method == "PUT":
            assert json.loads(request.content) == {"prefix": "allowed/"}
        if request.method == "DELETE":
            return httpx.Response(204)
        return httpx.Response(200, json={"items": [grant]} if request.method == "GET" else grant)

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        created = set_object_s3_copy_source.sync(
            "demo/x", bucket, credential, source, client=client, body=SetObjectS3CopySourceRequest(prefix="allowed/")
        )
        listed = list_object_s3_copy_sources.sync("demo/x", bucket, credential, client=client)
        assert isinstance(created, ObjectS3CopySource)
        assert isinstance(listed, ObjectS3CopySourceList)
        assert created.to_dict()["prefix"] == "allowed/"
        assert len(listed.items) == 1
        assert listed.items[0].to_dict() == created.to_dict()
        delete_object_s3_copy_source.sync_detailed("demo/x", bucket, credential, source, client=client)
        assert calls == ["PUT", "GET", "DELETE"]
