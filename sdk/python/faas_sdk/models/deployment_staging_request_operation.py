from typing import Literal

DeploymentStagingRequestOperation = Literal["restore", "stage"]

DEPLOYMENT_STAGING_REQUEST_OPERATION_VALUES: set[DeploymentStagingRequestOperation] = {
    "restore",
    "stage",
}


def check_deployment_staging_request_operation(value: str) -> DeploymentStagingRequestOperation:
    if value in DEPLOYMENT_STAGING_REQUEST_OPERATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_STAGING_REQUEST_OPERATION_VALUES!r}")
