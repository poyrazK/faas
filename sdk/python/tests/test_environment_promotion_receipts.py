"""adr: 20261105153007 — exact environment receipts carry no retry authority."""
from uuid import UUID

import pytest

from faas_sdk.preview.api.projects import (
    get_environment_promotion_receipt,
    get_org_environment_promotion_receipt,
    promote_project_environment,
    promote_org_project_environment,
)
from faas_sdk.preview.models.environment_promotion_receipt import EnvironmentPromotionReceipt
from faas_sdk.preview.models.promote_project_environment_request import PromoteProjectEnvironmentRequest


@pytest.mark.parametrize("company", [False, True])
def test_environment_promotion_attempt_transport_and_receipt(company: bool) -> None:
    attempt = "3e9f323aade6442b8444c91da107fe44"
    promotion = UUID(attempt)
    request_hash = "a" * 64
    selectors = ("team", "shop", "production") if company else ("shop", "production")
    write = promote_org_project_environment if company else promote_project_environment
    read = get_org_environment_promotion_receipt if company else get_environment_promotion_receipt
    kwargs = write._get_kwargs(*selectors, body=PromoteProjectEnvironmentRequest(from_environment="staging",promotion_token="private-preview"), idempotency_key="original", environment_promotion_attempt_id=attempt, environment_promotion_request_hash=request_hash)
    assert kwargs["headers"]["Environment-Promotion-Attempt-ID"] == attempt
    assert kwargs["headers"]["Environment-Promotion-Request-Hash"] == request_hash
    assert kwargs["headers"]["Idempotency-Key"] == "original"
    assert "Environment-Promotion-Attempt-ID" not in write._get_kwargs(*selectors, body=PromoteProjectEnvironmentRequest(from_environment="staging",promotion_token="private-preview"), idempotency_key="legacy")["headers"]
    prefix = "/v1/orgs/team/projects" if company else "/v1/projects"
    assert read._get_kwargs(*selectors, attempt) == {"method": "get", "url": f"{prefix}/shop/environments/production/promotion-attempts/{attempt}"}
    wire = {"attempt_id": attempt, "request_hash": request_hash, "promotion_id": str(promotion), "promotion_hash": "b" * 64, "project_id": "e377519c-cbca-4dfa-a3e7-26b1321e0de4", "from_environment_id": "8677f406-6b97-4b24-bf26-cf795e040713", "environment_id": "142b7504-f03a-4ee2-aeb3-14d922a845d4", "accepted_at": "2026-11-05T10:29:20Z"}
    receipt = EnvironmentPromotionReceipt.from_dict(wire)
    assert receipt.promotion_id == promotion
    assert receipt.environment_id == UUID(wire["environment_id"])
    assert receipt.to_dict() == {**wire, "accepted_at": "2026-11-05T10:29:20+00:00"}
