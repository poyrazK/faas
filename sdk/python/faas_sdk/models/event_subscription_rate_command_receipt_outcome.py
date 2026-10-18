from typing import Literal

EventSubscriptionRateCommandReceiptOutcome = Literal["applied", "rejected"]

EVENT_SUBSCRIPTION_RATE_COMMAND_RECEIPT_OUTCOME_VALUES: set[EventSubscriptionRateCommandReceiptOutcome] = {
    "applied",
    "rejected",
}


def check_event_subscription_rate_command_receipt_outcome(value: str) -> EventSubscriptionRateCommandReceiptOutcome:
    if value in EVENT_SUBSCRIPTION_RATE_COMMAND_RECEIPT_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_RATE_COMMAND_RECEIPT_OUTCOME_VALUES!r}"
    )
