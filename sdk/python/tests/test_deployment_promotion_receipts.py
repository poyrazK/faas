"""adr: 20261025090000 — historical receipt GET must not retry traffic."""
import httpx

from faas_sdk.api.deployments import get_deployment_promotion_receipt
from faas_sdk.client import Client


def test_promotion_receipt_read_has_no_mutation_body_or_header() -> None:
    attempt = "a" * 32
    kwargs = get_deployment_promotion_receipt._get_kwargs("alpha", attempt)
    assert kwargs == {
        "method": "get",
        "url": f"/v1/apps/alpha/deployment-promotions/{attempt}",
    }
    receipt = {
        "attempt_id": attempt,
        "request_hash": "b" * 64,
        "app_id": "11111111-1111-4111-8111-111111111111",
        "deployment_id": "22222222-2222-4222-8222-222222222222",
        "expected_serving_deployment_id": "",
        "scope": "default",
        "accepted_at": "2026-10-25T09:00:00Z",
    }
    parsed = get_deployment_promotion_receipt._parse_response(
        client=Client(base_url="https://example.test"),
        response=httpx.Response(200, json=receipt),
    )
    assert parsed.attempt_id == attempt
    assert str(parsed.deployment_id) == receipt["deployment_id"]
    assert parsed.expected_serving_deployment_id == ""
    assert "traffic_percent" not in parsed.to_dict()
