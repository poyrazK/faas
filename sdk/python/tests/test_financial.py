"""Retained financial read contracts through the generated SDK."""

import httpx
import pytest

from faas_sdk.api.billing import get_financial_costs, get_financial_forecast
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.financial_costs_response import FinancialCostsResponse
from faas_sdk.models.financial_forecast_response import FinancialForecastResponse
from faas_sdk.models.problem import Problem


@pytest.mark.parametrize("endpoint", [get_financial_costs, get_financial_forecast])
def test_financial_auth_month_money_and_gaps(endpoint) -> None:
    period = {
        "period_start": "2026-10-01T00:00:00Z",
        "period_end": "2026-11-01T00:00:00Z",
        "as_of": "2026-10-02T00:00:00Z",
        "currency": "EUR",
        "meters": [],
        "missing_bill_components": ["tax"],
    }

    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "GET"
        assert request.url.params["month"] == "2026-10"
        assert request.headers["Authorization"] == "Bearer financial-reader"
        assert not request.content
        if endpoint is get_financial_costs:
            assert request.url.path == "/v1/billing/costs"
            return httpx.Response(
                200,
                json={
                    **period,
                    "account_id": "account",
                    "retained_from": period["period_start"],
                    "evidence_through_id": 7,
                    "known_usage_millicents": 10001,
                    "scope": "retained_compute_and_interface_egress",
                    "invoices": [],
                    "invoice_reconciliation": "not_reconciled",
                },
            )
        assert request.url.path == "/v1/billing/forecast"
        return httpx.Response(200, json={**period, "bill_estimate_available": False})

    with AuthenticatedClient(
        base_url="https://api.example.com",
        token="financial-reader",
        httpx_args={"transport": httpx.MockTransport(respond)},
    ) as client:
        result = endpoint.sync(client=client, month="2026-10")
        if endpoint is get_financial_costs:
            assert isinstance(result, FinancialCostsResponse)
            assert result.known_usage_millicents == 10001
            assert result.invoice_reconciliation == "not_reconciled"
        else:
            assert isinstance(result, FinancialForecastResponse)
            assert result.bill_estimate_available is False
        assert result.missing_bill_components == ["tax"]
        problem = endpoint._parse_response(
            client=client,
            response=httpx.Response(
                503,
                json={
                    "status": 503,
                    "title": "Unavailable",
                    "type": "about:blank",
                    "detail": "Missing history",
                    "code": "capacity",
                },
            ),
        )
        assert isinstance(problem, Problem)
        assert problem.code == "capacity"


def test_budget_preview_preserves_intent_and_unavailable_enforcement() -> None:
    import json

    from faas_sdk.api.billing import preview_financial_budget
    from faas_sdk.models.financial_budget_preview_request import FinancialBudgetPreviewRequest
    from faas_sdk.models.financial_budget_preview_response import FinancialBudgetPreviewResponse

    spec = {
        "name": "Preview guard",
        "scope": {"kind": "account"},
        "currency": "EUR",
        "meters": ["compute"],
        "basis": "net_usage",
        "limit_millicents": 10001,
        "notify_millicents": [8000],
        "mode": "monitored",
        "action": "stop_previews",
        "drain_seconds": 30,
        "resume_rule": "manual",
        "enabled": True,
    }

    def respond(request: httpx.Request) -> httpx.Response:
        assert request.method == "POST"
        assert request.url.path == "/v1/billing/budgets/preview"
        assert json.loads(request.content) == {"spec": spec}
        assert request.headers["Authorization"] == "Bearer financial-reader"
        return httpx.Response(
            200,
            json={
                "spec": spec,
                "period_start": "2026-10-01T00:00:00Z",
                "period_end": "2026-11-01T00:00:00Z",
                "as_of": "2026-10-02T00:00:00Z",
                "known_millicents": 75,
                "known_limit_reached": False,
                "coverage_complete": False,
                "fresh": True,
                "reasons": ["enforcement_integration_pending"],
                "enforcement_ready": False,
                "guarantee": "monitored_after_retained_evidence",
                "targets": [
                    {"kind": "app", "id": "75a1d1bc-a2a4-4f29-bd95-d9f3c801c9e1", "name": "preview", "effect": "stop"}
                ],
                "continuing_targets": [
                    {
                        "kind": "app",
                        "id": "3916c1bf-f141-467a-806a-e4f25704e2d2",
                        "name": "production",
                        "effect": "compute_can_continue",
                    }
                ],
            },
        )

    with AuthenticatedClient(
        base_url="https://api.example.com",
        token="financial-reader",
        httpx_args={"transport": httpx.MockTransport(respond)},
    ) as client:
        result = preview_financial_budget.sync(
            client=client, body=FinancialBudgetPreviewRequest.from_dict({"spec": spec})
        )
        assert isinstance(result, FinancialBudgetPreviewResponse)
        assert result.enforcement_ready is False
        assert result.known_millicents == 75
        assert result.targets[0].name == "preview"
        assert result.continuing_targets[0].name == "production"
