"""Nested lock defaults retain typed fields and separate intent from observation."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import (
    get_object_bucket_object_lock,
    get_object_bucket_object_lock_capabilities,
    put_object_bucket_object_lock,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import (
    ObjectBucketObjectLock,
    ObjectBucketObjectLockConfiguration,
    ObjectBucketObjectLockRequest,
    ObjectLockCapabilities,
    ObjectLockDefaultRetention,
    ObjectRetentionPeriod,
)


def test_object_lock_nested_configuration() -> None:
    bucket = UUID("00000000-0000-0000-0000-000000000001")
    calls = []
    configuration = ObjectBucketObjectLockConfiguration(
        enabled=True,
        default_retention=ObjectLockDefaultRetention(
            mode="COMPLIANCE", days=3, default_event_hold=ObjectRetentionPeriod(years=1)
        ),
    )

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request.method)
        assert request.headers["Authorization"] == "Bearer token"
        assert request.url.path.startswith(f"/v1/apps/demo/buckets/{bucket}/object-lock")
        if request.url.path.endswith("-capabilities"):
            return httpx.Response(
                200,
                json={
                    "bucket_configuration": True,
                    "default_event_hold": True,
                    "version_retention": True,
                    "version_legal_hold": True,
                    "version_event_hold": True,
                    "write_event_hold": True,
                },
            )
        if request.method == "PUT":
            assert json.loads(request.content) == {"configuration": configuration.to_dict()}
        return httpx.Response(
            202 if request.method == "PUT" else 200,
            json={
                "bucket_id": str(bucket),
                "state": "waiting",
                "revision": 1,
                "enabled_required": True,
                "observed_known": False,
                "desired_configuration": configuration.to_dict(),
                "last_error_code": "versioning_pending",
                "updated_at": "2026-10-04T00:00:00Z",
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        caps = get_object_bucket_object_lock_capabilities.sync("demo", bucket, client=client)
        assert (
            isinstance(caps, ObjectLockCapabilities)
            and caps.default_event_hold
            and caps.version_event_hold
            and caps.write_event_hold
        )
        created = put_object_bucket_object_lock.sync(
            "demo", bucket, client=client, body=ObjectBucketObjectLockRequest(configuration=configuration)
        )
        read = get_object_bucket_object_lock.sync("demo", bucket, client=client)
        assert isinstance(created, ObjectBucketObjectLock)
        assert isinstance(read, ObjectBucketObjectLock)
        assert read.enabled_required and not read.observed_known
        assert read.to_dict()["desired_configuration"] == configuration.to_dict()
        assert calls == ["GET", "PUT", "GET"]
