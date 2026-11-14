from typing import Literal

ProjectEnvironmentConfigChangeKind = Literal["added", "changed", "removed"]

PROJECT_ENVIRONMENT_CONFIG_CHANGE_KIND_VALUES: set[ProjectEnvironmentConfigChangeKind] = {
    "added",
    "changed",
    "removed",
}


def check_project_environment_config_change_kind(value: str) -> ProjectEnvironmentConfigChangeKind:
    if value in PROJECT_ENVIRONMENT_CONFIG_CHANGE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_CONFIG_CHANGE_KIND_VALUES!r}")
