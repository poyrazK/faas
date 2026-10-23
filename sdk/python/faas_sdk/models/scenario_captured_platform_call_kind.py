from typing import Literal

ScenarioCapturedPlatformCallKind = Literal[
    "cron_run", "execution", "execution_cancel", "realtime_publish", "realtime_signal", "workflow_event", "workflow_run"
]

SCENARIO_CAPTURED_PLATFORM_CALL_KIND_VALUES: set[ScenarioCapturedPlatformCallKind] = {
    "cron_run",
    "execution",
    "execution_cancel",
    "realtime_publish",
    "realtime_signal",
    "workflow_event",
    "workflow_run",
}


def check_scenario_captured_platform_call_kind(value: str) -> ScenarioCapturedPlatformCallKind:
    if value in SCENARIO_CAPTURED_PLATFORM_CALL_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_CAPTURED_PLATFORM_CALL_KIND_VALUES!r}")
