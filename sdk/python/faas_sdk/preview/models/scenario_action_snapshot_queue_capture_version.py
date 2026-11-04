from typing import Literal

ScenarioActionSnapshotQueueCaptureVersion = Literal[1]

SCENARIO_ACTION_SNAPSHOT_QUEUE_CAPTURE_VERSION_VALUES: set[ScenarioActionSnapshotQueueCaptureVersion] = {
    1,
}


def check_scenario_action_snapshot_queue_capture_version(value: int) -> ScenarioActionSnapshotQueueCaptureVersion:
    if value in SCENARIO_ACTION_SNAPSHOT_QUEUE_CAPTURE_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCENARIO_ACTION_SNAPSHOT_QUEUE_CAPTURE_VERSION_VALUES!r}"
    )
