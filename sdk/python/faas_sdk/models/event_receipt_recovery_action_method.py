from typing import Literal

EventReceiptRecoveryActionMethod = Literal["POST"]

EVENT_RECEIPT_RECOVERY_ACTION_METHOD_VALUES: set[EventReceiptRecoveryActionMethod] = {
    "POST",
}


def check_event_receipt_recovery_action_method(value: str) -> EventReceiptRecoveryActionMethod:
    if value in EVENT_RECEIPT_RECOVERY_ACTION_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_RECOVERY_ACTION_METHOD_VALUES!r}")
