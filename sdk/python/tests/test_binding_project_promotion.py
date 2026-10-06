import httpx

from faas_sdk.api.projects import promote_project_environment_with_bindings
from faas_sdk.client import Client
from faas_sdk.models import ProjectEnvironmentPromotionResponse, PromoteProjectEnvironmentRequest


def test_checked_promotion_acceptance_and_route():
    body = PromoteProjectEnvironmentRequest(
        from_environment="staging", promotion_token="preview", require_bindings=True
    )
    kwargs = promote_project_environment_with_bindings._get_kwargs(
        "shop", "production", body=body, idempotency_key="stable"
    )
    assert kwargs["url"] == "/v1/projects/shop/environments/production/promote-with-bindings"
    assert kwargs["json"]["require_bindings"] is True
    assert kwargs["headers"]["Idempotency-Key"] == "stable"
    receipt = promote_project_environment_with_bindings._parse_response(
        client=Client(base_url="https://example.test"),
        response=httpx.Response(
            202,
            json={
                "promotion_id": "operation",
                "project_slug": "shop",
                "from_environment": "staging",
                "to_environment": "production",
                "promotion_hash": "hash",
                "workloads": [],
                "status": "running",
                "bindings_required": True,
            },
        ),
    )
    assert isinstance(receipt, ProjectEnvironmentPromotionResponse)
    assert receipt.bindings_required is True
    assert receipt.status == "running"
