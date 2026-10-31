from typing import Literal

EventReceiptRecoveryActionKind = Literal[
    "dead_letter_replay", "handler_replay", "keyed_handler_replay", "routing_replay"
]

EVENT_RECEIPT_RECOVERY_ACTION_KIND_VALUES: set[EventReceiptRecoveryActionKind] = {
    "dead_letter_replay",
    "handler_replay",
    "keyed_handler_replay",
    "routing_replay",
}


def check_event_receipt_recovery_action_kind(value: str) -> EventReceiptRecoveryActionKind:
    if value in EVENT_RECEIPT_RECOVERY_ACTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECEIPT_RECOVERY_ACTION_KIND_VALUES!r}")
