from uuid import UUID

import httpx

from faas_sdk.api.storage import get_object_bucket_encryption_capabilities
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectEncryptionCapabilities


def test_encryption_discovery():
    bucket = UUID("11111111-1111-4111-8111-111111111111")

    def handle(request):
        assert request.method == "GET"
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/encryption-capabilities"
        assert request.headers["Authorization"] == "Bearer token"
        return httpx.Response(
            200, json={"algorithms": ["AES256", "aws:kms"], "key_ids": ["owned-key"], "bucket_defaults": True}
        )

    client = AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    out = get_object_bucket_encryption_capabilities.sync(client=client, slug="demo", bucket=bucket)
    assert isinstance(out, ObjectEncryptionCapabilities)
    assert out.to_dict() == {"algorithms": ["AES256", "aws:kms"], "key_ids": ["owned-key"], "bucket_defaults": True}
