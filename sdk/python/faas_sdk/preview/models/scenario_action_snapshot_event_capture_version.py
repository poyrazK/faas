from typing import Literal

ScenarioActionSnapshotEventCaptureVersion = Literal[1]

SCENARIO_ACTION_SNAPSHOT_EVENT_CAPTURE_VERSION_VALUES: set[ScenarioActionSnapshotEventCaptureVersion] = {
    1,
}


def check_scenario_action_snapshot_event_capture_version(value: int) -> ScenarioActionSnapshotEventCaptureVersion:
    if value in SCENARIO_ACTION_SNAPSHOT_EVENT_CAPTURE_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCENARIO_ACTION_SNAPSHOT_EVENT_CAPTURE_VERSION_VALUES!r}"
    )
