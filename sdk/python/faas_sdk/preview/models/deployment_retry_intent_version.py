from typing import Literal

DeploymentRetryIntentVersion = Literal["v1"]

DEPLOYMENT_RETRY_INTENT_VERSION_VALUES: set[DeploymentRetryIntentVersion] = {
    "v1",
}


def check_deployment_retry_intent_version(value: str) -> DeploymentRetryIntentVersion:
    if value in DEPLOYMENT_RETRY_INTENT_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RETRY_INTENT_VERSION_VALUES!r}")
