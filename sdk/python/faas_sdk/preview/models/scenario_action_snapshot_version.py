from typing import Literal

ScenarioActionSnapshotVersion = Literal[1, 2]

SCENARIO_ACTION_SNAPSHOT_VERSION_VALUES: set[ScenarioActionSnapshotVersion] = {
    1,
    2,
}


def check_scenario_action_snapshot_version(value: int) -> ScenarioActionSnapshotVersion:
    if value in SCENARIO_ACTION_SNAPSHOT_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_ACTION_SNAPSHOT_VERSION_VALUES!r}")
