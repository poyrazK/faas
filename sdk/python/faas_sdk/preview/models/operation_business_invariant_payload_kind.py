from typing import Literal

OperationBusinessInvariantPayloadKind = Literal["gregale.business-invariant.v1"]

OPERATION_BUSINESS_INVARIANT_PAYLOAD_KIND_VALUES: set[OperationBusinessInvariantPayloadKind] = {
    "gregale.business-invariant.v1",
}


def check_operation_business_invariant_payload_kind(value: str) -> OperationBusinessInvariantPayloadKind:
    if value in OPERATION_BUSINESS_INVARIANT_PAYLOAD_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_BUSINESS_INVARIANT_PAYLOAD_KIND_VALUES!r}")
