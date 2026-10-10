from typing import Literal

DurableEntityValidatorDeploymentInfoStatus = Literal["disabled", "ready", "unavailable"]

DURABLE_ENTITY_VALIDATOR_DEPLOYMENT_INFO_STATUS_VALUES: set[DurableEntityValidatorDeploymentInfoStatus] = {
    "disabled",
    "ready",
    "unavailable",
}


def check_durable_entity_validator_deployment_info_status(value: str) -> DurableEntityValidatorDeploymentInfoStatus:
    if value in DURABLE_ENTITY_VALIDATOR_DEPLOYMENT_INFO_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DURABLE_ENTITY_VALIDATOR_DEPLOYMENT_INFO_STATUS_VALUES!r}"
    )
