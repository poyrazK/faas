from typing import Literal

ListEventSubscriptionCapacityCommandsOutcome = Literal["any", "applied", "unchanged"]

LIST_EVENT_SUBSCRIPTION_CAPACITY_COMMANDS_OUTCOME_VALUES: set[ListEventSubscriptionCapacityCommandsOutcome] = {
    "any",
    "applied",
    "unchanged",
}


def check_list_event_subscription_capacity_commands_outcome(value: str) -> ListEventSubscriptionCapacityCommandsOutcome:
    if value in LIST_EVENT_SUBSCRIPTION_CAPACITY_COMMANDS_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_EVENT_SUBSCRIPTION_CAPACITY_COMMANDS_OUTCOME_VALUES!r}"
    )
