from typing import Literal

CreateDeploymentRequestNativeRollout = Literal["service", "worker"]

CREATE_DEPLOYMENT_REQUEST_NATIVE_ROLLOUT_VALUES: set[CreateDeploymentRequestNativeRollout] = {
    "service",
    "worker",
}


def check_create_deployment_request_native_rollout(value: str) -> CreateDeploymentRequestNativeRollout:
    if value in CREATE_DEPLOYMENT_REQUEST_NATIVE_ROLLOUT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_DEPLOYMENT_REQUEST_NATIVE_ROLLOUT_VALUES!r}")
