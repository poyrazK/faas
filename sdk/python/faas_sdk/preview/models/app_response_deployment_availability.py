from typing import Literal

AppResponseDeploymentAvailability = Literal["live", "no_live_deployment"]

APP_RESPONSE_DEPLOYMENT_AVAILABILITY_VALUES: set[AppResponseDeploymentAvailability] = {
    "live",
    "no_live_deployment",
}


def check_app_response_deployment_availability(value: str) -> AppResponseDeploymentAvailability:
    if value in APP_RESPONSE_DEPLOYMENT_AVAILABILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_DEPLOYMENT_AVAILABILITY_VALUES!r}")
