from typing import Literal

EventSubscriptionCapacityCommandBudgetLimit = Literal[128]

EVENT_SUBSCRIPTION_CAPACITY_COMMAND_BUDGET_LIMIT_VALUES: set[EventSubscriptionCapacityCommandBudgetLimit] = {
    128,
}


def check_event_subscription_capacity_command_budget_limit(value: int) -> EventSubscriptionCapacityCommandBudgetLimit:
    if value in EVENT_SUBSCRIPTION_CAPACITY_COMMAND_BUDGET_LIMIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_CAPACITY_COMMAND_BUDGET_LIMIT_VALUES!r}"
    )
