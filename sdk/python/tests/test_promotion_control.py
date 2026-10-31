from uuid import UUID

import httpx

from faas_sdk.preview.api.projects import abort_project_environment_promotion, recover_project_environment_promotion
from faas_sdk.client import Client
from faas_sdk.preview.models.project_promotion_control import ProjectPromotionControl
from faas_sdk.preview.models.project_promotion_control_request import ProjectPromotionControlRequest


def test_promotion_controls_generation_and_accepted_receipts():
    request = ProjectPromotionControlRequest(request_id="a" * 32, expected_generation=7)
    promotion = UUID("00000000-0000-0000-0000-000000000001")
    client = Client(base_url="https://example.test")
    for action, operation in [
        ("abort", abort_project_environment_promotion),
        ("recover", recover_project_environment_promotion),
    ]:
        kwargs = operation._get_kwargs("shop", "production", promotion, body=request)
        assert kwargs["method"] == "post"
        assert kwargs["url"] == f"/v1/projects/shop/environments/production/promotions/{promotion}/{action}"
        assert kwargs["json"] == {"request_id": "a" * 32, "expected_generation": 7}
        for code, status in [(202, "pending"), (200, "restored")]:
            raw = {
                "request": request.to_dict(),
                "action": action,
                "status": status,
                "operator_account_id": "account",
                "requested_at": "2026-10-15T00:00:00Z",
                "deadline": "2026-10-15T00:10:00Z",
            }
            parsed = operation._parse_response(client=client, response=httpx.Response(code, json=raw))
            assert isinstance(parsed, ProjectPromotionControl)
            assert parsed.request.expected_generation == 7
            assert parsed.status == status
