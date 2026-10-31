from typing import Literal

ListEventRecoveriesMode = Literal["execution", "routing", "workflow"]

LIST_EVENT_RECOVERIES_MODE_VALUES: set[ListEventRecoveriesMode] = {
    "execution",
    "routing",
    "workflow",
}


def check_list_event_recoveries_mode(value: str) -> ListEventRecoveriesMode:
    if value in LIST_EVENT_RECOVERIES_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_EVENT_RECOVERIES_MODE_VALUES!r}")
