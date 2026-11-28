from typing import Literal

OperationBusinessCompensationPayloadKind = Literal["gregale.business-compensation.v1"]

OPERATION_BUSINESS_COMPENSATION_PAYLOAD_KIND_VALUES: set[OperationBusinessCompensationPayloadKind] = {
    "gregale.business-compensation.v1",
}


def check_operation_business_compensation_payload_kind(value: str) -> OperationBusinessCompensationPayloadKind:
    if value in OPERATION_BUSINESS_COMPENSATION_PAYLOAD_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_BUSINESS_COMPENSATION_PAYLOAD_KIND_VALUES!r}"
    )
