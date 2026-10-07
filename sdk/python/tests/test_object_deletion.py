"""Mutable deletion retains one intent across retries and receipt reads."""

from uuid import UUID

import httpx
import pytest

from faas_sdk.api.storage import create_object_deletion, get_object_deletion
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectDeletion, ObjectDeletionRequest


@pytest.mark.parametrize("selector", ["null", "12345678-1234-4234-8234-123456789abc"])
def test_deletion_receipt(selector: str) -> None:
    identity = UUID("ad7b43a1-4a3f-4f6d-9518-d1f0e4c79b78")
    receipt = {
        "id": str(identity),
        "bucket_id": str(identity),
        "key": "目录 /+%.txt",
        "selector": selector,
        "state": "dispatched",
        "delete_marker": False,
        "last_error_code": "provider_uncertain",
        "created_at": "2026-10-02T12:00:00Z",
        "updated_at": "2026-10-02T12:00:00Z",
    }

    def handle(request: httpx.Request) -> httpx.Response:
        if request.method == "POST":
            import json

            assert json.loads(request.content) == {"id": str(identity), "key": "目录 /+%.txt", "version_id": selector}
            return httpx.Response(202, json=receipt)
        assert request.url.path.endswith(f"/objects/deletions/{identity}")
        return httpx.Response(200, json=receipt)

    with httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.example.test", token="token").set_httpx_client(transport)
        j = create_object_deletion.sync(
            "demo",
            identity,
            client=client,
            body=ObjectDeletionRequest(id=identity, key="目录 /+%.txt", version_id=selector),
        )
        assert isinstance(j, ObjectDeletion)
        assert j.selector == selector
        assert j.state == "dispatched" and j.last_error_code == "provider_uncertain"
        j = get_object_deletion.sync("demo", identity, identity, client=client)
        assert isinstance(j, ObjectDeletion) and j.id == identity
