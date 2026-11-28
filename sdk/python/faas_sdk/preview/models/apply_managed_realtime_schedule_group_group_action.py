from typing import Literal

ApplyManagedRealtimeScheduleGroupGroupAction = Literal["cancel", "pause", "resume"]

APPLY_MANAGED_REALTIME_SCHEDULE_GROUP_GROUP_ACTION_VALUES: set[ApplyManagedRealtimeScheduleGroupGroupAction] = {
    "cancel",
    "pause",
    "resume",
}


def check_apply_managed_realtime_schedule_group_group_action(
    value: str,
) -> ApplyManagedRealtimeScheduleGroupGroupAction:
    if value in APPLY_MANAGED_REALTIME_SCHEDULE_GROUP_GROUP_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLY_MANAGED_REALTIME_SCHEDULE_GROUP_GROUP_ACTION_VALUES!r}"
    )
