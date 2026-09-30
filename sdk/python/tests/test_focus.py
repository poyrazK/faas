"""FOCUS artifact bytes and Problem parsing through the generated SDK."""

import httpx
import pytest

from faas_sdk.api.billing import export_focus_invoices
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.problem import Problem
from faas_sdk.types import File


@pytest.mark.parametrize(
    "format_,content_type", [("zip", "application/zip"), ("csv", "text/csv"), ("metadata", "application/json")]
)
def test_focus_download_preserves_bytes_and_auth(format_: str, content_type: str) -> None:
    data = b"PK\x00\xff\x80\n" if format_ == "zip" else b'{"ConformanceStatus":"partial"}\n'

    def respond(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/v1/billing/focus"
        assert request.url.params["month"] == "2026-09"
        assert request.url.params["format"] == format_
        assert request.headers["Authorization"] == "Bearer focus-token"
        return httpx.Response(200, content=data, headers={"Content-Type": content_type})

    with AuthenticatedClient(
        base_url="https://api.example.com",
        token="focus-token",
        httpx_args={"transport": httpx.MockTransport(respond)},
    ) as client:
        response = export_focus_invoices.sync_detailed(client=client, month="2026-09", format_=format_)
        assert response.content == data
        assert isinstance(response.parsed, File)
        assert response.parsed.payload.read() == data


def test_focus_download_retains_problem() -> None:
    client = AuthenticatedClient(base_url="https://api.example.com", token="focus-token")
    response = httpx.Response(
        409,
        json={
            "status": 409,
            "title": "Export unavailable",
            "code": "conflict",
            "type": "about:blank",
            "detail": "invalid invoice",
        },
    )
    parsed = export_focus_invoices._parse_response(client=client, response=response)
    assert isinstance(parsed, Problem)
    assert parsed.code == "conflict"


def test_focus_refresh_authentication_and_source_gap() -> None:
    from uuid import UUID

    from faas_sdk.api.billing import refresh_invoice_facts
    from faas_sdk.models.invoice_refresh_response import InvoiceRefreshResponse

    invoice_id = UUID("c4979a3e-345b-4a96-a635-321589233f7f")

    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == f"/v1/invoices/{invoice_id}/refresh"
        assert not request.url.query
        assert request.content == b""
        assert request.headers["Authorization"] == "Bearer focus-token"
        return httpx.Response(
            200,
            json={
                "invoice_id": str(invoice_id),
                "provider": "polar",
                "detailed": False,
                "line_items": 2,
                "source_gap": "unclassified",
                "updated_at": "2026-10-01T00:00:00Z",
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.com",
        token="focus-token",
        httpx_args={"transport": httpx.MockTransport(respond)},
    ) as client:
        result = refresh_invoice_facts.sync(invoice_id, client=client)
        assert isinstance(result, InvoiceRefreshResponse)
        assert result.invoice_id == invoice_id
        assert result.source_gap == "unclassified"
        problem = refresh_invoice_facts._parse_response(
            client=client,
            response=httpx.Response(
                409,
                json={
                    "status": 409,
                    "title": "Refresh conflict",
                    "code": "conflict",
                    "type": "about:blank",
                    "detail": "Snapshot changed",
                },
            ),
        )
        assert isinstance(problem, Problem)
        assert problem.code == "conflict"
