"""Typed receipt reads preserve pending status and pagination without writes."""

from uuid import UUID

import httpx

from faas_sdk.api.storage import get_object_write_receipt, list_object_write_receipts
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import ObjectWriteReceipt, ObjectWriteReceiptList


def test_receipt_status_and_pending_page() -> None:
    bucket = UUID("00000000-0000-0000-0000-000000000001")
    receipt = UUID("00000000-0000-0000-0000-000000000002")
    value = {
        "id": str(receipt),
        "bucket_id": str(bucket),
        "key": "destination",
        "operation": "copy",
        "bytes": 5,
        "content_type": "image/png",
        "etag": "",
        "status": "pending",
        "error_code": "provider_write_uncertain",
        "created_at": "2026-10-01T00:00:00Z",
    }
    calls = []

    def handle(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        calls.append(request)
        if request.url.path.endswith(str(receipt)):
            return httpx.Response(200, json=value, headers={"Retry-After": "30"})
        assert request.url.params["status"] == "pending"
        assert request.url.params["limit"] == "2"
        assert request.url.params["cursor"] == "next+cursor"
        return httpx.Response(200, json={"items": [value], "next_cursor": "another"})

    with httpx.Client(base_url="https://api.example.test", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.example.test", token="test-token").set_httpx_client(
            transport
        )
        result = get_object_write_receipt.sync_detailed("demo", bucket, receipt, client=client)
        assert isinstance(result.parsed, ObjectWriteReceipt)
        assert result.parsed.status == "pending"
        assert result.parsed.error_code == "provider_write_uncertain"
        assert result.headers["Retry-After"] == "30"
        page = list_object_write_receipts.sync(
            "demo", bucket, client=client, status="pending", limit=2, cursor="next+cursor"
        )
        assert isinstance(page, ObjectWriteReceiptList)
        assert page.next_cursor == "another"
        assert page.items[0].to_dict()["id"] == str(receipt)
        assert len(calls) == 2
