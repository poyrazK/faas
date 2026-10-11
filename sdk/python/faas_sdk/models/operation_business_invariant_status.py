from typing import Literal

OperationBusinessInvariantStatus = Literal["failed", "passed", "unknown"]

OPERATION_BUSINESS_INVARIANT_STATUS_VALUES: set[OperationBusinessInvariantStatus] = {
    "failed",
    "passed",
    "unknown",
}


def check_operation_business_invariant_status(value: str) -> OperationBusinessInvariantStatus:
    if value in OPERATION_BUSINESS_INVARIANT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_BUSINESS_INVARIANT_STATUS_VALUES!r}")
