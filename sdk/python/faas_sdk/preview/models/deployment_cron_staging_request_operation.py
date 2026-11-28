from typing import Literal

DeploymentCronStagingRequestOperation = Literal["cancel", "prepare", "restore", "stage", "track_upload"]

DEPLOYMENT_CRON_STAGING_REQUEST_OPERATION_VALUES: set[DeploymentCronStagingRequestOperation] = {
    "cancel",
    "prepare",
    "restore",
    "stage",
    "track_upload",
}


def check_deployment_cron_staging_request_operation(value: str) -> DeploymentCronStagingRequestOperation:
    if value in DEPLOYMENT_CRON_STAGING_REQUEST_OPERATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_CRON_STAGING_REQUEST_OPERATION_VALUES!r}")
