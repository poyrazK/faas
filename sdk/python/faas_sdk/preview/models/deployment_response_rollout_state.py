from typing import Literal

DeploymentResponseRolloutState = Literal["aborted", "complete", "pending", "rolling_out"]

DEPLOYMENT_RESPONSE_ROLLOUT_STATE_VALUES: set[DeploymentResponseRolloutState] = {
    "aborted",
    "complete",
    "pending",
    "rolling_out",
}


def check_deployment_response_rollout_state(value: str) -> DeploymentResponseRolloutState:
    if value in DEPLOYMENT_RESPONSE_ROLLOUT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RESPONSE_ROLLOUT_STATE_VALUES!r}")
