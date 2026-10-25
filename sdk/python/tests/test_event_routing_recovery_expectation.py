from uuid import UUID

from faas_sdk.preview.models.event_routing_recovery_expectation import EventRoutingRecoveryExpectation
from faas_sdk.preview.models.replay_event_fanout_failure_request import ReplayEventFanoutFailureRequest


def test_guarded_routing_replay_round_trip():
    expectation = EventRoutingRecoveryExpectation(
        workflow_name="paid", published_version=3, replay_count=0, generation=0
    )
    request = ReplayEventFanoutFailureRequest(
        event_id="evt-1",
        event_source="billing",
        subscription_id=UUID("11111111-1111-4111-8111-111111111111"),
        recovery_expectation=expectation,
    )
    body = request.to_dict()
    assert body["recovery_expectation"] == {
        "workflow_name": "paid",
        "published_version": 3,
        "replay_count": 0,
        "generation": 0,
    }
    assert ReplayEventFanoutFailureRequest.from_dict(body).to_dict() == body


def test_legacy_guard_omits_generation_and_unguarded_requests_omit_expectations():
    expectation = EventRoutingRecoveryExpectation(workflow_name="paid", published_version=3, replay_count=2)
    assert "generation" not in expectation.to_dict()
    request = ReplayEventFanoutFailureRequest(
        event_id="evt-1", event_source="billing", subscription_id=UUID("11111111-1111-4111-8111-111111111111")
    )
    assert "recovery_expectation" not in request.to_dict()
