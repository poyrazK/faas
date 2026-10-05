from typing import Literal

OperationDeliveryResponseState = Literal[
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

OPERATION_DELIVERY_RESPONSE_STATE_VALUES: set[OperationDeliveryResponseState] = {
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


def check_operation_delivery_response_state(value: str) -> OperationDeliveryResponseState:
    if value in OPERATION_DELIVERY_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DELIVERY_RESPONSE_STATE_VALUES!r}")
