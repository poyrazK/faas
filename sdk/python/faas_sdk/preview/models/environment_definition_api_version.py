from typing import Literal

EnvironmentDefinitionApiVersion = Literal["gregale.dev/environment/v1"]

ENVIRONMENT_DEFINITION_API_VERSION_VALUES: set[EnvironmentDefinitionApiVersion] = {
    "gregale.dev/environment/v1",
}


def check_environment_definition_api_version(value: str) -> EnvironmentDefinitionApiVersion:
    if value in ENVIRONMENT_DEFINITION_API_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_DEFINITION_API_VERSION_VALUES!r}")
