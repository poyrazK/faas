from typing import Literal

BindingRuntimeDeploymentStatus = Literal["current", "inactive", "stale", "unknown", "updating"]

BINDING_RUNTIME_DEPLOYMENT_STATUS_VALUES: set[BindingRuntimeDeploymentStatus] = {
    "current",
    "inactive",
    "stale",
    "unknown",
    "updating",
}


def check_binding_runtime_deployment_status(value: str) -> BindingRuntimeDeploymentStatus:
    if value in BINDING_RUNTIME_DEPLOYMENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_RUNTIME_DEPLOYMENT_STATUS_VALUES!r}")
