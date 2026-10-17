from typing import Literal

DeploymentCompanionsResponseReason = Literal["declaration_metadata_unavailable"]

DEPLOYMENT_COMPANIONS_RESPONSE_REASON_VALUES: set[DeploymentCompanionsResponseReason] = {
    "declaration_metadata_unavailable",
}


def check_deployment_companions_response_reason(value: str) -> DeploymentCompanionsResponseReason:
    if value in DEPLOYMENT_COMPANIONS_RESPONSE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_COMPANIONS_RESPONSE_REASON_VALUES!r}")
