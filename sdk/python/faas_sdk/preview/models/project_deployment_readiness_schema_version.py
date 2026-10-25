from typing import Literal

ProjectDeploymentReadinessSchemaVersion = Literal[1]

PROJECT_DEPLOYMENT_READINESS_SCHEMA_VERSION_VALUES: set[ProjectDeploymentReadinessSchemaVersion] = {
    1,
}


def check_project_deployment_readiness_schema_version(value: int) -> ProjectDeploymentReadinessSchemaVersion:
    if value in PROJECT_DEPLOYMENT_READINESS_SCHEMA_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_DEPLOYMENT_READINESS_SCHEMA_VERSION_VALUES!r}"
    )
