from typing import Literal

ProjectEnvironmentDomainDiffResponseKind = Literal["changed", "unchanged"]

PROJECT_ENVIRONMENT_DOMAIN_DIFF_RESPONSE_KIND_VALUES: set[ProjectEnvironmentDomainDiffResponseKind] = {
    "changed",
    "unchanged",
}


def check_project_environment_domain_diff_response_kind(value: str) -> ProjectEnvironmentDomainDiffResponseKind:
    if value in PROJECT_ENVIRONMENT_DOMAIN_DIFF_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_DOMAIN_DIFF_RESPONSE_KIND_VALUES!r}"
    )
