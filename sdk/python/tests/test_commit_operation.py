"""The shared operation route decodes both managed and Commit receipts."""

from uuid import UUID

import httpx
import pytest

from faas_sdk.api.exclusive_operations import get_exclusive_operation
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.commit_operation_response import CommitOperationResponse
from faas_sdk.models.exclusive_operation_record import ExclusiveOperationRecord


@pytest.mark.parametrize(
    ("fields", "expected"),
    [
        (
            {
                "receipt_id": "d4829eac-b7c7-4eba-8849-310f251151ba",
                "source_id": "6c585dbb-4f86-42c6-aed6-9f384ab1d712",
                "event_id": "890c99d4-41f6-4a2d-b605-33d6e9177349",
                "accepted_at": "2026-10-01T12:00:00Z",
            },
            CommitOperationResponse,
        ),
        (
            {"sequence": 1, "policy_revision": 1, "generation": 1, "created_at": "2026-10-01T12:00:00Z"},
            ExclusiveOperationRecord,
        ),
    ],
)
def test_shared_operation_route_decodes_receipt(fields, expected):
    identity = UUID("7910a14a-6f35-48e9-9a56-a8b11a5bbf37")
    wire = {
        "id": str(identity),
        "state": "completed",
        "completed_at": "2026-10-01T12:01:00Z",
        "result": {"order_id": 123},
        "effects": [
            {
                "id": str(identity),
                "delivery_id": str(identity),
                "webhook_id": str(identity),
                "name": "notify",
                "generation": 1,
                "type": "order.fulfilled",
                "status": "dead",
                "attempt": 2,
            }
        ],
        **fields,
    }

    def respond(request):
        assert request.method == "GET"
        assert request.url.path == f"/v1/operations/{identity}"
        assert request.headers["authorization"] == "Bearer fixture-token"
        return httpx.Response(200, json=wire)

    client = AuthenticatedClient(
        base_url="https://api.example", token="fixture-token", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    with client.get_httpx_client():
        result = get_exclusive_operation.sync(identity, client=client)
    assert isinstance(result, expected)
    assert result.id == identity
    assert result.state == "completed"
    assert result.completed_at.isoformat() == "2026-10-01T12:01:00+00:00"

    assert result.result == {"order_id": 123}
    assert len(result.effects) == 1
    assert result.effects[0].delivery_id == identity
    assert result.effects[0].status == "dead"
    assert result.effects[0].attempt == 2
