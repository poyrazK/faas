from typing import Literal

OperationDeliveryInspectionBusinessState = Literal[
    "accepted", "cancelled", "failed", "requires_reconciliation", "running", "succeeded"
]

OPERATION_DELIVERY_INSPECTION_BUSINESS_STATE_VALUES: set[OperationDeliveryInspectionBusinessState] = {
    "accepted",
    "cancelled",
    "failed",
    "requires_reconciliation",
    "running",
    "succeeded",
}


def check_operation_delivery_inspection_business_state(value: str) -> OperationDeliveryInspectionBusinessState:
    if value in OPERATION_DELIVERY_INSPECTION_BUSINESS_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_DELIVERY_INSPECTION_BUSINESS_STATE_VALUES!r}"
    )
