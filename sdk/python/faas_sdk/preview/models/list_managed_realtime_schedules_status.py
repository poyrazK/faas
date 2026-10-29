from typing import Literal

ListManagedRealtimeSchedulesStatus = Literal["canceled", "failed", "paused", "pending", "published", "skipped"]

LIST_MANAGED_REALTIME_SCHEDULES_STATUS_VALUES: set[ListManagedRealtimeSchedulesStatus] = {
    "canceled",
    "failed",
    "paused",
    "pending",
    "published",
    "skipped",
}


def check_list_managed_realtime_schedules_status(value: str) -> ListManagedRealtimeSchedulesStatus:
    if value in LIST_MANAGED_REALTIME_SCHEDULES_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_MANAGED_REALTIME_SCHEDULES_STATUS_VALUES!r}")
