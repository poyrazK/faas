from typing import Literal

ProjectEnvironmentDomainResponseOwnership = Literal["application", "environment"]

PROJECT_ENVIRONMENT_DOMAIN_RESPONSE_OWNERSHIP_VALUES: set[ProjectEnvironmentDomainResponseOwnership] = {
    "application",
    "environment",
}


def check_project_environment_domain_response_ownership(value: str) -> ProjectEnvironmentDomainResponseOwnership:
    if value in PROJECT_ENVIRONMENT_DOMAIN_RESPONSE_OWNERSHIP_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_DOMAIN_RESPONSE_OWNERSHIP_VALUES!r}"
    )
