from typing import Literal

OperatorRuntimeConfigRolloutState = Literal["canary", "paused", "promoting", "rolled_back", "stable"]

OPERATOR_RUNTIME_CONFIG_ROLLOUT_STATE_VALUES: set[OperatorRuntimeConfigRolloutState] = {
    "canary",
    "paused",
    "promoting",
    "rolled_back",
    "stable",
}


def check_operator_runtime_config_rollout_state(value: str) -> OperatorRuntimeConfigRolloutState:
    if value in OPERATOR_RUNTIME_CONFIG_ROLLOUT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATOR_RUNTIME_CONFIG_ROLLOUT_STATE_VALUES!r}")
