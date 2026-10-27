from typing import Literal

ProjectMigrationBuildWaitStatus = Literal["failed", "ready", "waiting"]

PROJECT_MIGRATION_BUILD_WAIT_STATUS_VALUES: set[ProjectMigrationBuildWaitStatus] = {
    "failed",
    "ready",
    "waiting",
}


def check_project_migration_build_wait_status(value: str) -> ProjectMigrationBuildWaitStatus:
    if value in PROJECT_MIGRATION_BUILD_WAIT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_MIGRATION_BUILD_WAIT_STATUS_VALUES!r}")
