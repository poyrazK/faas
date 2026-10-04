"""Tag operations preserve current, null and owned version selectors."""

import json
from uuid import UUID

import httpx
import pytest

from faas_sdk.api.storage import delete_object_bucket_tags, get_object_bucket_tags, put_object_bucket_tags
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectTaggingRequest, ObjectTaggingRequestTags, ObjectTaggingResult
from faas_sdk.types import UNSET, Unset


@pytest.mark.parametrize("version", [UNSET, "null", "ad7b43a1-4a3f-4f6d-9518-d1f0e4c79b78"])
def test_tagging_selector(version: str | Unset) -> None:
    bucket = UUID("ad7b43a1-4a3f-4f6d-9518-d1f0e4c79b78")
    key, tags = "目录 /+%.txt", {"team name": "old & value"}
    methods: list[str] = []

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/objects/tags"
        assert request.url.params["key"] == key
        assert request.url.params.get("version_id") == (None if version is UNSET else version)
        assert request.headers["Authorization"] == "Bearer token"
        methods.append(request.method)
        if request.method == "PUT":
            assert json.loads(request.content) == {"tags": tags}
        response = {"tags": {} if request.method == "DELETE" else tags}
        if version is not UNSET:
            response["version_id"] = version
        return httpx.Response(200, json=response)

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        read = get_object_bucket_tags.sync("demo", bucket, client=client, key=key, version_id=version)
        assert isinstance(read, ObjectTaggingResult)
        assert read.tags.to_dict() == tags
        written = put_object_bucket_tags.sync(
            "demo",
            bucket,
            client=client,
            key=key,
            version_id=version,
            body=ObjectTaggingRequest(tags=ObjectTaggingRequestTags.from_dict(tags)),
        )
        assert isinstance(written, ObjectTaggingResult)
        assert written.version_id == version
        cleared = delete_object_bucket_tags.sync("demo", bucket, client=client, key=key, version_id=version)
        assert isinstance(cleared, ObjectTaggingResult)
        assert cleared.tags.to_dict() == {}
        assert methods == ["GET", "PUT", "DELETE"]
