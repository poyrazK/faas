"""Customer lifecycle configuration and discovery preserve typed wire data."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import (
    create_object_lifecycle_scan,
    delete_object_bucket_lifecycle,
    get_object_bucket_lifecycle,
    get_object_lifecycle_scan,
    put_object_bucket_lifecycle,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import (
    ObjectBucketLifecycle,
    ObjectBucketLifecycleRequest,
    ObjectLifecycleRule,
    ObjectLifecycleScan,
)


def test_object_lifecycle_configuration_and_scan() -> None:
    bucket = UUID("00000000-0000-0000-0000-000000000001")
    scan = UUID("00000000-0000-0000-0000-000000000002")
    calls = []
    rules = [{"status": "Enabled", "id": "abandoned", "abort_incomplete_multipart_days": 2}]

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request.method)
        assert request.headers["Authorization"] == "Bearer token"
        assert request.url.path.startswith(f"/v1/apps/demo/buckets/{bucket}/lifecycle")
        if request.method == "PUT":
            assert json.loads(request.content) == {"rules": rules}
        result = {"bucket_id": str(bucket), "revision": 1, "updated_at": "2026-10-03T00:00:00Z"}
        if "/scans" in request.url.path:
            result.update(
                id=str(scan),
                state="completed",
                phase="multipart",
                scanned_keys=0,
                scanned_uploads=3,
                created_at="2026-10-03T00:00:00Z",
            )
        else:
            result["rules"] = [] if request.method == "DELETE" else rules
        return httpx.Response(202 if request.method == "POST" else 200, json=result)

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        body = ObjectBucketLifecycleRequest(
            rules=[ObjectLifecycleRule(status="Enabled", id="abandoned", abort_incomplete_multipart_days=2)]
        )
        put = put_object_bucket_lifecycle.sync("demo", bucket, client=client, body=body)
        get = get_object_bucket_lifecycle.sync("demo", bucket, client=client)
        created = create_object_lifecycle_scan.sync("demo", bucket, client=client)
        read = get_object_lifecycle_scan.sync("demo", bucket, scan, client=client)
        clear = delete_object_bucket_lifecycle.sync("demo", bucket, client=client)
        assert isinstance(put, ObjectBucketLifecycle) and isinstance(get, ObjectBucketLifecycle)
        assert get.to_dict()["rules"] == rules
        assert isinstance(created, ObjectLifecycleScan) and isinstance(read, ObjectLifecycleScan)
        assert read.scanned_uploads == 3 and read.phase == "multipart"
        assert isinstance(clear, ObjectBucketLifecycle) and clear.rules == []
        assert calls == ["PUT", "GET", "POST", "GET", "DELETE"]
