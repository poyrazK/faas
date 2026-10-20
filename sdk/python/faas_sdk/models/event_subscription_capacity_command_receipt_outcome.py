from typing import Literal

EventSubscriptionCapacityCommandReceiptOutcome = Literal["applied", "unchanged"]

EVENT_SUBSCRIPTION_CAPACITY_COMMAND_RECEIPT_OUTCOME_VALUES: set[EventSubscriptionCapacityCommandReceiptOutcome] = {
    "applied",
    "unchanged",
}


def check_event_subscription_capacity_command_receipt_outcome(
    value: str,
) -> EventSubscriptionCapacityCommandReceiptOutcome:
    if value in EVENT_SUBSCRIPTION_CAPACITY_COMMAND_RECEIPT_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_CAPACITY_COMMAND_RECEIPT_OUTCOME_VALUES!r}"
    )
