from typing import Literal

ScenarioActionSnapshotQueueDeliveryVersion = Literal[1]

SCENARIO_ACTION_SNAPSHOT_QUEUE_DELIVERY_VERSION_VALUES: set[ScenarioActionSnapshotQueueDeliveryVersion] = {
    1,
}


def check_scenario_action_snapshot_queue_delivery_version(value: int) -> ScenarioActionSnapshotQueueDeliveryVersion:
    if value in SCENARIO_ACTION_SNAPSHOT_QUEUE_DELIVERY_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCENARIO_ACTION_SNAPSHOT_QUEUE_DELIVERY_VERSION_VALUES!r}"
    )
