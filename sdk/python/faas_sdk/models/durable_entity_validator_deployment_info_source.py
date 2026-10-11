from typing import Literal

DurableEntityValidatorDeploymentInfoSource = Literal["object_storage", "registry"]

DURABLE_ENTITY_VALIDATOR_DEPLOYMENT_INFO_SOURCE_VALUES: set[DurableEntityValidatorDeploymentInfoSource] = {
    "object_storage",
    "registry",
}


def check_durable_entity_validator_deployment_info_source(value: str) -> DurableEntityValidatorDeploymentInfoSource:
    if value in DURABLE_ENTITY_VALIDATOR_DEPLOYMENT_INFO_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DURABLE_ENTITY_VALIDATOR_DEPLOYMENT_INFO_SOURCE_VALUES!r}"
    )
