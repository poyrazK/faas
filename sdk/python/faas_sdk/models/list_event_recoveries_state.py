from typing import Literal

ListEventRecoveriesState = Literal["cancelled", "completed", "paused", "running"]

LIST_EVENT_RECOVERIES_STATE_VALUES: set[ListEventRecoveriesState] = {
    "cancelled",
    "completed",
    "paused",
    "running",
}


def check_list_event_recoveries_state(value: str) -> ListEventRecoveriesState:
    if value in LIST_EVENT_RECOVERIES_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_EVENT_RECOVERIES_STATE_VALUES!r}")
