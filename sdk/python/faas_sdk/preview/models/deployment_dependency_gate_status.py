from typing import Literal

DeploymentDependencyGateStatus = Literal["failed", "ready", "waiting"]

DEPLOYMENT_DEPENDENCY_GATE_STATUS_VALUES: set[DeploymentDependencyGateStatus] = {
    "failed",
    "ready",
    "waiting",
}


def check_deployment_dependency_gate_status(value: str) -> DeploymentDependencyGateStatus:
    if value in DEPLOYMENT_DEPENDENCY_GATE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_DEPENDENCY_GATE_STATUS_VALUES!r}")
