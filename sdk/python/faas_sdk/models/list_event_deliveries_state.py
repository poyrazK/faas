from typing import Literal

ListEventDeliveriesState = Literal["completed", "dead_letter", "dispatching", "failed", "pending"]

LIST_EVENT_DELIVERIES_STATE_VALUES: set[ListEventDeliveriesState] = {
    "completed",
    "dead_letter",
    "dispatching",
    "failed",
    "pending",
}


def check_list_event_deliveries_state(value: str) -> ListEventDeliveriesState:
    if value in LIST_EVENT_DELIVERIES_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_EVENT_DELIVERIES_STATE_VALUES!r}")
