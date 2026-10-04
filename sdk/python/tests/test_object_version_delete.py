"""Permanent deletion uses an owned immutable public selector."""

from uuid import UUID

import httpx

from faas_sdk.api.storage import delete_object_bucket_version
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectVersionDeleteResult


def test_owned_version_deletion() -> None:
    bucket = UUID("ad7b43a1-4a3f-4f6d-9518-d1f0e4c79b78")
    version = UUID("7ddfe5e3-11f5-46f8-9a76-787cff7a315e")
    key = "目录 /+%.txt"

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.method == "DELETE"
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/objects/versions"
        assert request.url.params["key"] == key
        assert request.url.params["version_id"] == str(version)
        return httpx.Response(200, json={"version_id": str(version), "delete_marker": True})

    with httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.example.test", token="token").set_httpx_client(transport)
        result = delete_object_bucket_version.sync("demo", bucket, client=client, key=key, version_id=str(version))
        assert isinstance(result, ObjectVersionDeleteResult)
        assert result.version_id == str(version)
        assert result.delete_marker is True


def test_native_inventory_progress_is_typed() -> None:
    from faas_sdk.api.storage import get_object_capacity_reconciliation
    from faas_sdk.models import ObjectCapacityReconciliation

    bucket = UUID("ad7b43a1-4a3f-4f6d-9518-d1f0e4c79b78")
    job = UUID("7ddfe5e3-11f5-46f8-9a76-787cff7a315e")
    progress = {
        "id": str(job),
        "bucket_id": str(bucket),
        "state": "scanning",
        "inventory_scope": "all_versions",
        "scanned_pages": 3,
        "scanned_bytes": 17,
        "scanned_versions": 2,
        "before_bytes": 100,
        "before_keys": 3,
        "after_bytes": 100,
        "after_keys": 3,
        "reclaimed_bytes": 0,
        "reclaimed_keys": 0,
        "pending_writes": 0,
        "created_at": "2026-10-02T10:00:00Z",
        "updated_at": "2026-10-02T10:00:00Z",
    }

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/capacity-reconciliations/{job}"
        return httpx.Response(200, json=progress)

    with httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.example.test", token="token").set_httpx_client(transport)
        result = get_object_capacity_reconciliation.sync("demo", bucket, job, client=client)
        assert isinstance(result, ObjectCapacityReconciliation)
        assert result.inventory_scope == "all_versions"
        assert result.scanned_pages == 3
        assert result.scanned_bytes == 17
        assert result.scanned_versions == 2
