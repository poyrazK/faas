from typing import Literal

EventSubscriptionCapacityCommandsOutcome = Literal["any", "applied", "unchanged"]

EVENT_SUBSCRIPTION_CAPACITY_COMMANDS_OUTCOME_VALUES: set[EventSubscriptionCapacityCommandsOutcome] = {
    "any",
    "applied",
    "unchanged",
}


def check_event_subscription_capacity_commands_outcome(value: str) -> EventSubscriptionCapacityCommandsOutcome:
    if value in EVENT_SUBSCRIPTION_CAPACITY_COMMANDS_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_CAPACITY_COMMANDS_OUTCOME_VALUES!r}"
    )
