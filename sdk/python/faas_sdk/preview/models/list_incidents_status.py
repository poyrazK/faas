from typing import Literal

ListIncidentsStatus = Literal["all", "open"]

LIST_INCIDENTS_STATUS_VALUES: set[ListIncidentsStatus] = {
    "all",
    "open",
}


def check_list_incidents_status(value: str) -> ListIncidentsStatus:
    if value in LIST_INCIDENTS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_INCIDENTS_STATUS_VALUES!r}")
