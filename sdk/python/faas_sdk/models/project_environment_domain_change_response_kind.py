from typing import Literal

ProjectEnvironmentDomainChangeResponseKind = Literal["added", "changed", "removed"]

PROJECT_ENVIRONMENT_DOMAIN_CHANGE_RESPONSE_KIND_VALUES: set[ProjectEnvironmentDomainChangeResponseKind] = {
    "added",
    "changed",
    "removed",
}


def check_project_environment_domain_change_response_kind(value: str) -> ProjectEnvironmentDomainChangeResponseKind:
    if value in PROJECT_ENVIRONMENT_DOMAIN_CHANGE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_DOMAIN_CHANGE_RESPONSE_KIND_VALUES!r}"
    )
