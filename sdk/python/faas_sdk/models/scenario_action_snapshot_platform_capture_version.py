from typing import Literal

ScenarioActionSnapshotPlatformCaptureVersion = Literal[1]

SCENARIO_ACTION_SNAPSHOT_PLATFORM_CAPTURE_VERSION_VALUES: set[ScenarioActionSnapshotPlatformCaptureVersion] = {
    1,
}


def check_scenario_action_snapshot_platform_capture_version(value: int) -> ScenarioActionSnapshotPlatformCaptureVersion:
    if value in SCENARIO_ACTION_SNAPSHOT_PLATFORM_CAPTURE_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCENARIO_ACTION_SNAPSHOT_PLATFORM_CAPTURE_VERSION_VALUES!r}"
    )
