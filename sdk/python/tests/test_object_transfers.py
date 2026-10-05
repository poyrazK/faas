"""Configured upload discovery remains compatible with older catalogs."""

import httpx
import pytest

from faas_sdk.api.storage import list_object_buckets
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectBucketList
from faas_sdk.types import UNSET


@pytest.mark.parametrize("modern", [True, False])
def test_bucket_transfer_discovery(modern):
    catalog = {
        "items": [],
        "enabled": True,
        "regions": ["us-east-1"],
        "default_region": "us-east-1",
        "max_upload_bytes": 5 << 40,
        "max_buckets_per_app": 10,
    }
    if modern:
        catalog.update(
            max_single_put_bytes=512 << 20,
            max_part_bytes=512 << 20,
            transfer_timeout_seconds=7200,
            upload_profile="direct",
        )

    def handle(request):
        assert request.url.path == "/v1/apps/demo/buckets"
        assert request.headers["Authorization"] == "Bearer token"
        return httpx.Response(200, json=catalog)

    client = AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    got = list_object_buckets.sync(slug="demo", client=client)
    assert isinstance(got, ObjectBucketList) and got.to_dict() == catalog
    assert got.upload_profile == "direct" if modern else got.upload_profile is UNSET
