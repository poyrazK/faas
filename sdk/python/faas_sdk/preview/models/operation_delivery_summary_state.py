from typing import Literal

OperationDeliverySummaryState = Literal[
    "awaiting_outcome",
    "configuration_failed",
    "dead",
    "delivery_expired",
    "failed",
    "in_flight",
    "not_requested",
    "pending",
    "succeeded",
]

OPERATION_DELIVERY_SUMMARY_STATE_VALUES: set[OperationDeliverySummaryState] = {
    "awaiting_outcome",
    "configuration_failed",
    "dead",
    "delivery_expired",
    "failed",
    "in_flight",
    "not_requested",
    "pending",
    "succeeded",
}


def check_operation_delivery_summary_state(value: str) -> OperationDeliverySummaryState:
    if value in OPERATION_DELIVERY_SUMMARY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DELIVERY_SUMMARY_STATE_VALUES!r}")
