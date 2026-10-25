from typing import Literal

OperationBusinessDecisionPayloadKind = Literal["gregale.business-decision.v1"]

OPERATION_BUSINESS_DECISION_PAYLOAD_KIND_VALUES: set[OperationBusinessDecisionPayloadKind] = {
    "gregale.business-decision.v1",
}


def check_operation_business_decision_payload_kind(value: str) -> OperationBusinessDecisionPayloadKind:
    if value in OPERATION_BUSINESS_DECISION_PAYLOAD_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_BUSINESS_DECISION_PAYLOAD_KIND_VALUES!r}")
