from typing import Literal

ListAdminStatusEventsKind = Literal["incident", "maintenance"]

LIST_ADMIN_STATUS_EVENTS_KIND_VALUES: set[ListAdminStatusEventsKind] = {
    "incident",
    "maintenance",
}


def check_list_admin_status_events_kind(value: str) -> ListAdminStatusEventsKind:
    if value in LIST_ADMIN_STATUS_EVENTS_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ADMIN_STATUS_EVENTS_KIND_VALUES!r}")
