from typing import Literal

ScenarioActionSnapshotRecorderVersion = Literal[1]

SCENARIO_ACTION_SNAPSHOT_RECORDER_VERSION_VALUES: set[ScenarioActionSnapshotRecorderVersion] = {
    1,
}


def check_scenario_action_snapshot_recorder_version(value: int) -> ScenarioActionSnapshotRecorderVersion:
    if value in SCENARIO_ACTION_SNAPSHOT_RECORDER_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_ACTION_SNAPSHOT_RECORDER_VERSION_VALUES!r}")
