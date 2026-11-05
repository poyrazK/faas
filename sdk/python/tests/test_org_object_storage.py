import httpx

from faas_sdk.preview.api.storage import (
    list_org_object_buckets,
    list_org_object_storage_compute_bindings,
    revoke_org_object_s3_credential,
)
from faas_sdk.client import AuthenticatedClient


def test_company_object_storage_preserves_ownership_paths() -> None:
    paths: list[tuple[str, bytes]] = []

    def respond(request: httpx.Request) -> httpx.Response:
        paths.append((request.method, request.url.raw_path))
        assert request.headers["authorization"] == "Bearer company-token"
        if request.method == "DELETE":
            return httpx.Response(204)
        return httpx.Response(200, json={"items": [], "enabled": True, "regions": ["us-east-1"],
                                          "default_region": "us-east-1", "max_upload_bytes": 100, "max_buckets_per_app": 10})

    client = AuthenticatedClient(
        base_url="https://api.example.test",
        token="company-token",
        httpx_args={"transport": httpx.MockTransport(respond)},
    )
    with client:
        buckets = list_org_object_buckets.sync(
            organization="my company", slug="app/id", client=client
        )
        assert buckets is not None and buckets.items == []
        bindings = list_org_object_storage_compute_bindings.sync(
            organization="my company", slug="app/id", bucket="bucket/id", client=client
        )
        assert bindings is not None and bindings.items == []
        revoked = revoke_org_object_s3_credential.sync_detailed(
            organization="my company", slug="app/id", bucket="bucket/id",
            credential="key/id", client=client,
        )
        assert revoked.status_code == 204
    assert paths == [
        ("GET", b"/v1/orgs/my%20company/apps/app%2Fid/buckets"),
        ("GET", b"/v1/orgs/my%20company/apps/app%2Fid/buckets/bucket%2Fid/compute-bindings"),
        ("DELETE", b"/v1/orgs/my%20company/apps/app%2Fid/buckets/bucket%2Fid/s3-credentials/key%2Fid"),
    ]
