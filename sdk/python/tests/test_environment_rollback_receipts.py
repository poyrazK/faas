"""adr: 20261105102920 — exact environment receipts carry no retry authority."""
from uuid import UUID

import pytest

from faas_sdk.preview.api.projects import (
    get_environment_rollback_receipt,
    get_org_environment_rollback_receipt,
    rollback_project_environment_promotion,
    rollback_org_project_environment_promotion,
)
from faas_sdk.preview.models.environment_rollback_receipt import EnvironmentRollbackReceipt


@pytest.mark.parametrize("company", [False, True])
def test_environment_rollback_attempt_transport_and_receipt(company: bool) -> None:
    attempt = "3e9f323aade6442b8444c91da107fe44"
    promotion = UUID(attempt)
    request_hash = "a" * 64
    selectors = ("team", "shop", "production", promotion) if company else ("shop", "production", promotion)
    write = rollback_org_project_environment_promotion if company else rollback_project_environment_promotion
    read = get_org_environment_rollback_receipt if company else get_environment_rollback_receipt
    kwargs = write._get_kwargs(*selectors, idempotency_key="original", environment_rollback_attempt_id=attempt, environment_rollback_request_hash=request_hash)
    assert kwargs["headers"]["Environment-Rollback-Attempt-ID"] == attempt
    assert kwargs["headers"]["Environment-Rollback-Request-Hash"] == request_hash
    assert kwargs["headers"]["Idempotency-Key"] == "original"
    assert "Environment-Rollback-Attempt-ID" not in write._get_kwargs(*selectors, idempotency_key="legacy")["headers"]
    prefix = "/v1/orgs/team/projects" if company else "/v1/projects"
    assert read._get_kwargs(*selectors, attempt) == {"method": "get", "url": f"{prefix}/shop/environments/production/promotions/{promotion}/rollback-attempts/{attempt}"}
    wire = {"attempt_id": attempt, "request_hash": request_hash, "promotion_id": str(promotion), "promotion_hash": "b" * 64, "project_id": "e377519c-cbca-4dfa-a3e7-26b1321e0de4", "from_environment_id": "8677f406-6b97-4b24-bf26-cf795e040713", "environment_id": "142b7504-f03a-4ee2-aeb3-14d922a845d4", "accepted_at": "2026-11-05T10:29:20Z"}
    receipt = EnvironmentRollbackReceipt.from_dict(wire)
    assert receipt.promotion_id == promotion
    assert receipt.environment_id == UUID(wire["environment_id"])
    assert receipt.to_dict() == {**wire, "accepted_at": "2026-11-05T10:29:20+00:00"}
