from typing import Literal

ScenarioActionSnapshotSimulationVersion = Literal[1]

SCENARIO_ACTION_SNAPSHOT_SIMULATION_VERSION_VALUES: set[ScenarioActionSnapshotSimulationVersion] = {
    1,
}


def check_scenario_action_snapshot_simulation_version(value: int) -> ScenarioActionSnapshotSimulationVersion:
    if value in SCENARIO_ACTION_SNAPSHOT_SIMULATION_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCENARIO_ACTION_SNAPSHOT_SIMULATION_VERSION_VALUES!r}"
    )
