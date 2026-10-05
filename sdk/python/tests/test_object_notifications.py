"""Notification clients preserve the owned destination and decoded key filter."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import (
    delete_object_bucket_notifications,
    get_object_bucket_notifications,
    put_object_bucket_notifications,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectBucketNotifications, ObjectBucketNotificationsRequest


def test_notification_configuration():
    bucket = UUID("11111111-1111-4111-8111-111111111111")
    rules = [
        {
            "id": "images",
            "destination": "arn:gregale:sqs:us-east-1:11111111-1111-4111-8111-111111111111:22222222-2222-4222-8222-222222222222/storage",
            "events": ["s3:ObjectCreated:Put"],
            "prefix": "images/目录",
        }
    ]
    calls = []

    def handle(request):
        calls.append(request.method)
        assert request.headers["Authorization"] == "Bearer token"
        assert request.url.path == f"/v1/apps/demo/buckets/{bucket}/notifications"
        if request.method == "PUT":
            assert json.loads(request.content) == {"rules": rules}
        return httpx.Response(
            200, json={"bucket_id": str(bucket), "revision": 2, "rules": [] if request.method == "DELETE" else rules}
        )

    client = AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    )
    params = {"client": client, "slug": "demo", "bucket": bucket}
    put = put_object_bucket_notifications.sync(
        **params, body=ObjectBucketNotificationsRequest.from_dict({"rules": rules})
    )
    get = get_object_bucket_notifications.sync(**params)
    clear = delete_object_bucket_notifications.sync(**params)
    assert (
        isinstance(put, ObjectBucketNotifications)
        and isinstance(get, ObjectBucketNotifications)
        and isinstance(clear, ObjectBucketNotifications)
    )
    assert get.to_dict()["rules"] == rules
    assert clear.rules == [] and calls == ["PUT", "GET", "DELETE"]
