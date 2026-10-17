from typing import Literal

ScenarioCapturedActionSource = Literal["operation", "workflow"]

SCENARIO_CAPTURED_ACTION_SOURCE_VALUES: set[ScenarioCapturedActionSource] = {
    "operation",
    "workflow",
}


def check_scenario_captured_action_source(value: str) -> ScenarioCapturedActionSource:
    if value in SCENARIO_CAPTURED_ACTION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_CAPTURED_ACTION_SOURCE_VALUES!r}")
