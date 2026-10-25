from typing import Literal

DeploymentResponseCanaryPreset = Literal["1-10-50-100", "adaptive", "aggressive", "balanced", "none", "slow"]

DEPLOYMENT_RESPONSE_CANARY_PRESET_VALUES: set[DeploymentResponseCanaryPreset] = {
    "1-10-50-100",
    "adaptive",
    "aggressive",
    "balanced",
    "none",
    "slow",
}


def check_deployment_response_canary_preset(value: str) -> DeploymentResponseCanaryPreset:
    if value in DEPLOYMENT_RESPONSE_CANARY_PRESET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RESPONSE_CANARY_PRESET_VALUES!r}")
