from typing import Literal

ScenarioActionSnapshotScope = Literal["managed_operation_webhooks", "managed_webhooks_and_simulated_outbound"]

SCENARIO_ACTION_SNAPSHOT_SCOPE_VALUES: set[ScenarioActionSnapshotScope] = {
    "managed_operation_webhooks",
    "managed_webhooks_and_simulated_outbound",
}


def check_scenario_action_snapshot_scope(value: str) -> ScenarioActionSnapshotScope:
    if value in SCENARIO_ACTION_SNAPSHOT_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_ACTION_SNAPSHOT_SCOPE_VALUES!r}")
