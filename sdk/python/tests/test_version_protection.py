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
