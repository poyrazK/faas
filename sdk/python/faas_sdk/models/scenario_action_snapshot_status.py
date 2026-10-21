from typing import Literal

ScenarioActionSnapshotStatus = Literal["complete", "incomplete", "pending"]

SCENARIO_ACTION_SNAPSHOT_STATUS_VALUES: set[ScenarioActionSnapshotStatus] = {
    "complete",
    "incomplete",
    "pending",
}


def check_scenario_action_snapshot_status(value: str) -> ScenarioActionSnapshotStatus:
    if value in SCENARIO_ACTION_SNAPSHOT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_ACTION_SNAPSHOT_STATUS_VALUES!r}")
