from typing import Literal

EventSubscriptionResponseWorkAction = Literal["cancel_pending", "invoke"]

EVENT_SUBSCRIPTION_RESPONSE_WORK_ACTION_VALUES: set[EventSubscriptionResponseWorkAction] = {
    "cancel_pending",
    "invoke",
}


def check_event_subscription_response_work_action(value: str) -> EventSubscriptionResponseWorkAction:
    if value in EVENT_SUBSCRIPTION_RESPONSE_WORK_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_RESPONSE_WORK_ACTION_VALUES!r}")
