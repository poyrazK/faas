from typing import Literal

EnvironmentCacheScheduleEventAction = Literal["inheritance", "policy", "purge"]

ENVIRONMENT_CACHE_SCHEDULE_EVENT_ACTION_VALUES: set[EnvironmentCacheScheduleEventAction] = {
    "inheritance",
    "policy",
    "purge",
}


def check_environment_cache_schedule_event_action(value: str) -> EnvironmentCacheScheduleEventAction:
    if value in ENVIRONMENT_CACHE_SCHEDULE_EVENT_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_CACHE_SCHEDULE_EVENT_ACTION_VALUES!r}")
