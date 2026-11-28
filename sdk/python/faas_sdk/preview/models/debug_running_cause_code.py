from typing import Literal

DebugRunningCauseCode = Literal[
    "activity_unknown",
    "min_instances",
    "no_blocker_observed",
    "open_connection",
    "prewarm_floor",
    "request_activity",
    "scale_in_cooldown",
    "startup_grace",
    "tail_tasks",
    "workload_mode",
]

DEBUG_RUNNING_CAUSE_CODE_VALUES: set[DebugRunningCauseCode] = {
    "activity_unknown",
    "min_instances",
    "no_blocker_observed",
    "open_connection",
    "prewarm_floor",
    "request_activity",
    "scale_in_cooldown",
    "startup_grace",
    "tail_tasks",
    "workload_mode",
}


def check_debug_running_cause_code(value: str) -> DebugRunningCauseCode:
    if value in DEBUG_RUNNING_CAUSE_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_RUNNING_CAUSE_CODE_VALUES!r}")
