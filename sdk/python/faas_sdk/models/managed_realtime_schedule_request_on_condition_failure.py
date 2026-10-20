from typing import Literal

ManagedRealtimeScheduleRequestOnConditionFailure = Literal["retry", "skip"]

MANAGED_REALTIME_SCHEDULE_REQUEST_ON_CONDITION_FAILURE_VALUES: set[ManagedRealtimeScheduleRequestOnConditionFailure] = {
    "retry",
    "skip",
}


def check_managed_realtime_schedule_request_on_condition_failure(
    value: str,
) -> ManagedRealtimeScheduleRequestOnConditionFailure:
    if value in MANAGED_REALTIME_SCHEDULE_REQUEST_ON_CONDITION_FAILURE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_SCHEDULE_REQUEST_ON_CONDITION_FAILURE_VALUES!r}"
    )
