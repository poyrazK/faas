from typing import Literal

DeploymentBuildDependencyGateStatus = Literal["failed", "ready", "waiting"]

DEPLOYMENT_BUILD_DEPENDENCY_GATE_STATUS_VALUES: set[DeploymentBuildDependencyGateStatus] = {
    "failed",
    "ready",
    "waiting",
}


def check_deployment_build_dependency_gate_status(value: str) -> DeploymentBuildDependencyGateStatus:
    if value in DEPLOYMENT_BUILD_DEPENDENCY_GATE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_BUILD_DEPENDENCY_GATE_STATUS_VALUES!r}")
