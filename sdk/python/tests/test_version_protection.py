"""Exact owned version selectors and stable protection operation identities."""

import json
from uuid import UUID

import httpx

from faas_sdk.api.storage import (
    get_object_version_legal_hold,
    get_object_version_protection,
    get_object_version_retention,
    put_object_version_legal_hold,
    put_object_version_retention,
)
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import (
    ObjectVersionLegalHold,
    ObjectVersionLegalHoldRequest,
    ObjectVersionProtection,
    ObjectVersionRetention,
    ObjectVersionRetentionRequest,
)


def test_version_protection_roundtrip() -> None:
    bucket = UUID("00000000-0000-4000-8000-000000000001")
    operation = UUID("00000000-0000-4000-8000-000000000002")
    version = "00000000-0000-4000-8000-000000000003"
    key = "目录/+ %"
    calls = []
    receipt = {
        "id": str(operation),
        "bucket_id": str(bucket),
        "key": key,
        "version_id": version,
        "kind": "legal_hold",
        "state": "waiting",
        "legal_hold": {"status": "OFF"},
        "created_at": "2026-10-04T00:00:00Z",
        "updated_at": "2026-10-04T00:00:00Z",
    }

    def handle(request: httpx.Request) -> httpx.Response:
        calls.append(request.method)
        assert request.headers["Authorization"] == "Bearer token"
        if "protection-operations" in request.url.path:
            assert request.url.path.endswith(str(operation))
            return httpx.Response(200, json={**receipt, "state": "ready"})
        assert request.url.params["key"] == key
        assert request.url.params["version_id"] == version
        if request.method == "PUT":
            body = json.loads(request.content)
            assert body["id"] == str(operation)
            return httpx.Response(202, json=receipt)
        if request.url.path.endswith("retention"):
            return httpx.Response(200, json={"version_id": version, "retention": {}})
        return httpx.Response(200, json={"version_id": version, "legal_hold": {"status": "ON"}})

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        read = get_object_version_legal_hold.sync("demo", bucket, client=client, key=key, version_id=version)
        assert read is not None and read.legal_hold.status == "ON"
        accepted = put_object_version_legal_hold.sync(
            "demo",
            bucket,
            client=client,
            key=key,
            version_id=version,
            body=ObjectVersionLegalHoldRequest(id=operation, legal_hold=ObjectVersionLegalHold(status="OFF")),
        )
        assert isinstance(accepted, ObjectVersionProtection) and accepted.state == "waiting"
        get_object_version_retention.sync("demo", bucket, client=client, key=key, version_id=version)
        put_object_version_retention.sync(
            "demo",
            bucket,
            client=client,
            key=key,
            version_id=version,
            body=ObjectVersionRetentionRequest(id=operation, retention=ObjectVersionRetention()),
        )
        done = get_object_version_protection.sync("demo", bucket, operation, client=client)
        assert isinstance(done, ObjectVersionProtection) and done.state == "ready"
    assert len(calls) == 5


def test_event_hold_protection_requests() -> None:
    from faas_sdk.models import ObjectRetentionPeriod

    operation = UUID("00000000-0000-4000-8000-000000000001")
    bucket = UUID("00000000-0000-4000-8000-000000000002")
    policies = [
        ObjectVersionRetention(mode="COMPLIANCE", event_hold="ON", event_hold_duration=ObjectRetentionPeriod(days=30)),
        ObjectVersionRetention(mode="GOVERNANCE", event_hold="ON", event_hold_duration=ObjectRetentionPeriod(years=1)),
        ObjectVersionRetention(mode="COMPLIANCE", event_hold="OFF"),
    ]
    calls = []

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.url.params["version_id"] == "null"
        assert request.url.params["key"] == "目录/+ %"
        body = json.loads(request.content)
        assert body == {"id": str(operation), "retention": policies[len(calls)].to_dict()}
        calls.append(body)
        return httpx.Response(
            202,
            json={
                "id": str(operation),
                "bucket_id": str(bucket),
                "key": "目录/+ %",
                "version_id": "null",
                "kind": "retention",
                "state": "waiting",
                "retention": body["retention"],
                "created_at": "2026-10-05T00:00:00Z",
                "updated_at": "2026-10-05T00:00:00Z",
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(handle)}
    ) as client:
        for policy in policies:
            out = put_object_version_retention.sync(
                "demo",
                bucket,
                client=client,
                key="目录/+ %",
                version_id="null",
                body=ObjectVersionRetentionRequest(id=operation, retention=policy),
            )
            assert isinstance(out, ObjectVersionProtection) and out.retention.to_dict() == policy.to_dict()
    assert len(calls) == len(policies)
