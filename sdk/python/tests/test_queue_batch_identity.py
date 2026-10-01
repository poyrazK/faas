from uuid import UUID

from faas_sdk.models.dispatch_invocation_batch_body import DispatchInvocationBatchBody
from faas_sdk.models.dispatch_invocation_batch_body_records_item import DispatchInvocationBatchBodyRecordsItem
from faas_sdk.models.dispatch_invocation_batch_response_200 import DispatchInvocationBatchResponse200


def test_durable_queue_batch_identity_round_trip() -> None:
    invocation_id = UUID("33333333-3333-3333-3333-333333333333")
    wire = {
        "item_identifier": str(invocation_id),
        "payload_b64": "e30=",
        "invocation_id": str(invocation_id),
        "invocation_attempt": 2,
    }
    record = DispatchInvocationBatchBodyRecordsItem.from_dict(wire)
    assert record.invocation_id == invocation_id
    assert record.invocation_attempt == 2
    assert record.to_dict() == wire


def test_broker_batch_has_no_durable_identity() -> None:
    record = DispatchInvocationBatchBodyRecordsItem(item_identifier="broker-record", payload_b64="e30=")
    assert record.to_dict() == {"item_identifier": "broker-record", "payload_b64": "e30="}


def test_batch_contract_has_actual_envelope_and_per_record_results() -> None:
    body = {
        "invocation_id": "trigger-test",
        "app_id": "22222222-2222-2222-2222-222222222222",
        "trigger_id": "11111111-1111-1111-1111-111111111111",
        "source": "esm",
        "records": [{"item_identifier": "broker-record", "payload_b64": "e30="}],
    }
    assert DispatchInvocationBatchBody.from_dict(body).to_dict() == body
    response = {"results": [{"item_identifier": "broker-record", "status": "retry", "code": "invoke_timeout"}]}
    assert DispatchInvocationBatchResponse200.from_dict(response).to_dict() == response
