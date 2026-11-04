from typing import Literal

OperationBusinessCompensationStatus = Literal["confirmed", "failed", "pending", "required"]

OPERATION_BUSINESS_COMPENSATION_STATUS_VALUES: set[OperationBusinessCompensationStatus] = {
    "confirmed",
    "failed",
    "pending",
    "required",
}


def check_operation_business_compensation_status(value: str) -> OperationBusinessCompensationStatus:
    if value in OPERATION_BUSINESS_COMPENSATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_BUSINESS_COMPENSATION_STATUS_VALUES!r}")
