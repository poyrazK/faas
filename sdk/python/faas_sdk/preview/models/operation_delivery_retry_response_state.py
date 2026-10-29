from typing import Literal

OperationDeliveryRetryResponseState = Literal["queued"]

OPERATION_DELIVERY_RETRY_RESPONSE_STATE_VALUES: set[OperationDeliveryRetryResponseState] = {
    "queued",
}


def check_operation_delivery_retry_response_state(value: str) -> OperationDeliveryRetryResponseState:
    if value in OPERATION_DELIVERY_RETRY_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_DELIVERY_RETRY_RESPONSE_STATE_VALUES!r}")
