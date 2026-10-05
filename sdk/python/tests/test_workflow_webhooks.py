import json

import httpx
import pytest

from faas_sdk import AuthenticatedClient
from faas_sdk.api.inbound_webhooks import (
    delete_webhook_automation_binding,
    get_webhook_automation_binding,
    get_webhook_automation_receipt,
    put_webhook_automation_binding,
    receive_inbound_webhook,
)
from faas_sdk.models.inbound_webhook_receipt_response import InboundWebhookReceiptResponse
from faas_sdk.models.put_webhook_automation_binding_request import PutWebhookAutomationBindingRequest
from faas_sdk.models.webhook_automation_receipt_response import WebhookAutomationReceiptResponse
from faas_sdk.models.workflow_callback_webhook_receipt_response import WorkflowCallbackWebhookReceiptResponse


@pytest.mark.parametrize("status", ["accepted", "ignored"])
def test_provider_receipt_selects_automation_model(status):
    endpoint = "00000000-0000-0000-0000-000000000001"
    body = {
        "receipt_id": endpoint,
        "endpoint_id": endpoint,
        "provider_event_id": "evt_1",
        "workflow_name": "paid",
        "status": status,
        "duplicate": False,
        "accepted_at": "2026-10-03T12:00:00Z",
        "event_source": "gregale.inbound.stripe." + endpoint,
        "routing_status": "pending" if status == "accepted" else "ignored",
    }
    client = AuthenticatedClient(base_url="https://api.example.com", token="token")
    parsed = receive_inbound_webhook._parse_response(client=client, response=httpx.Response(202, json=body))
    assert isinstance(parsed, WebhookAutomationReceiptResponse)
    assert parsed.provider_event_id == "evt_1"


def test_provider_receipt_preserves_invocation_and_callback_models():
    endpoint = "00000000-0000-0000-0000-000000000001"
    client = AuthenticatedClient(base_url="https://api.example.com", token="token")
    for body, model in [
        (
            {"receipt_id": endpoint, "status": "accepted", "duplicate": False, "accepted_at": "2026-10-03T12:00:00Z"},
            InboundWebhookReceiptResponse,
        ),
        ({"callback_id": endpoint, "status": "received", "duplicate": False}, WorkflowCallbackWebhookReceiptResponse),
    ]:
        parsed = receive_inbound_webhook._parse_response(client=client, response=httpx.Response(202, json=body))
        assert isinstance(parsed, model)


def test_webhook_binding_and_receipt_routes():
    endpoint = "00000000-0000-0000-0000-000000000001"
    binding = {
        "endpoint_id": endpoint,
        "workflow_name": "paid",
        "event_type": "invoice.*",
        "filter": {},
        "version": 7,
        "updated_at": "2026-10-03T12:00:00Z",
    }
    calls = []

    def handle(request):
        calls.append(request)
        if request.method == "DELETE":
            assert request.url.params["expected_version"] == "7"
            return httpx.Response(204)
        if request.url.path.endswith("/automation-receipts/evt_1"):
            return httpx.Response(
                200,
                json={
                    "receipt_id": endpoint,
                    "endpoint_id": endpoint,
                    "provider_event_id": "evt_1",
                    "workflow_name": "paid",
                    "status": "accepted",
                    "duplicate": False,
                    "accepted_at": "2026-10-03T12:00:00Z",
                    "event_source": "gregale.inbound.stripe." + endpoint,
                    "routing_status": "enqueued",
                    "run_id": endpoint,
                },
            )
        assert request.url.path == "/v1/apps/billing/inbound-webhooks/" + endpoint + "/automation-binding"
        if request.method == "PUT":
            assert json.loads(request.content) == {
                "expected_version": 0,
                "workflow_name": "paid",
                "event_type": "invoice.*",
                "take_over_delivery": True,
            }
        return httpx.Response(200, json=binding)

    client = AuthenticatedClient(base_url="https://api.example.com", token="token")
    body = PutWebhookAutomationBindingRequest(
        expected_version=0, workflow_name="paid", event_type="invoice.*", take_over_delivery=True
    )
    with httpx.Client(base_url="https://api.example.com", transport=httpx.MockTransport(handle)) as transport:
        client.set_httpx_client(transport)
        saved = put_webhook_automation_binding.sync(slug="billing", id=endpoint, client=client, body=body)
        assert saved.version == 7
        assert get_webhook_automation_binding.sync(slug="billing", id=endpoint, client=client).workflow_name == "paid"
        receipt = get_webhook_automation_receipt.sync(slug="billing", id=endpoint, event_id="evt_1", client=client)
        assert str(receipt.run_id) == endpoint
        assert (
            delete_webhook_automation_binding.sync_detailed(
                slug="billing", id=endpoint, expected_version=7, client=client
            ).status_code
            == 204
        )
    assert len(calls) == 4
