from typing import Literal

DeploymentCronActivationStatusStatus = Literal["activated", "conflicted", "discarded", "not_managed", "pending"]

DEPLOYMENT_CRON_ACTIVATION_STATUS_STATUS_VALUES: set[DeploymentCronActivationStatusStatus] = {
    "activated",
    "conflicted",
    "discarded",
    "not_managed",
    "pending",
}


def check_deployment_cron_activation_status_status(value: str) -> DeploymentCronActivationStatusStatus:
    if value in DEPLOYMENT_CRON_ACTIVATION_STATUS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_CRON_ACTIVATION_STATUS_STATUS_VALUES!r}")
