from typing import Literal

ListEventSubscriptionDeadlinesStatus = Literal["all", "expired", "upcoming"]

LIST_EVENT_SUBSCRIPTION_DEADLINES_STATUS_VALUES: set[ListEventSubscriptionDeadlinesStatus] = {
    "all",
    "expired",
    "upcoming",
}


def check_list_event_subscription_deadlines_status(value: str) -> ListEventSubscriptionDeadlinesStatus:
    if value in LIST_EVENT_SUBSCRIPTION_DEADLINES_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_EVENT_SUBSCRIPTION_DEADLINES_STATUS_VALUES!r}")
