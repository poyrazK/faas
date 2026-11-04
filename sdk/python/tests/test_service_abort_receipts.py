"""adr: 20261104173000 — exact service-abort acceptance and scoped metadata."""
import pytest
from uuid import UUID

from faas_sdk.api.deployments import get_service_abort_receipt, recover_rollout
from faas_sdk.models.app_release_review import AppReleaseReview
from faas_sdk.models.recover_rollout_request import RecoverRolloutRequest
from faas_sdk.models.service_abort_receipt import ServiceAbortReceipt


@pytest.mark.parametrize("scope", ["default", "staging"])
def test_service_abort_attempt_transport_and_read_only_receipt(scope: str) -> None:
    attempt = "3e9f323aade6442b8444c91da107fe44"
    app = UUID("142b7504-f03a-4ee2-aeb3-14d922a845d4")
    candidate = UUID("a2b9cc53-907f-4b5c-88a4-fd0c21214556")
    predecessor = UUID("5b87c415-7c93-4932-acab-a3c90e98be86")
    review = AppReleaseReview(
        app_id=app, configuration_hash="review", latest_deployment_id=str(candidate),
        serving_deployment_ids=[str(predecessor)],
    )
    if scope == "staging":
        review.scope = scope
        review.project_id = UUID("e377519c-cbca-4dfa-a3e7-26b1321e0de4")
        review.environment_id = UUID("8677f406-6b97-4b24-bf26-cf795e040713")
    body = RecoverRolloutRequest(
        action="abort", deployment_id=candidate,
        expected_predecessor_deployment_id=predecessor, expected_review=review,
    )
    kwargs = recover_rollout._get_kwargs("alpha", body=body, service_abort_attempt_id=attempt)
    assert kwargs["headers"]["Service-Abort-Attempt-ID"] == attempt
    assert kwargs["json"]["expected_review"] == review.to_dict()
    assert "Service-Abort-Attempt-ID" not in recover_rollout._get_kwargs("alpha", body=body)["headers"]
    assert get_service_abort_receipt._get_kwargs("alpha", attempt) == {
        "method": "get", "url": f"/v1/apps/alpha/service-aborts/{attempt}"
    }
    wire = {
        "attempt_id": attempt, "request_hash": "a" * 64, "app_id": str(app),
        "deployment_id": str(candidate), "predecessor_deployment_id": str(predecessor),
        "request_id": "d5c0a595-f6d7-415e-a9d8-d6634052f03a", "scope": scope,
        "accepted_at": "2026-11-04T14:40:00Z",
    }
    if scope == "staging":
        wire.update(project_id=str(review.project_id), environment_id=str(review.environment_id))
    receipt = ServiceAbortReceipt.from_dict(wire)
    if scope == "staging":
        assert receipt.project_id == review.project_id and receipt.environment_id == review.environment_id
        assert receipt.to_dict()["environment_id"] == str(review.environment_id)
    assert receipt.attempt_id == attempt and str(receipt.request_id) == wire["request_id"]
    assert receipt.to_dict()["predecessor_deployment_id"] == str(predecessor)
