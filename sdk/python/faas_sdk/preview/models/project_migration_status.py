from typing import Literal

ProjectMigrationStatus = Literal["applying", "attention", "prepared", "tracking"]

PROJECT_MIGRATION_STATUS_VALUES: set[ProjectMigrationStatus] = {
    "applying",
    "attention",
    "prepared",
    "tracking",
}


def check_project_migration_status(value: str) -> ProjectMigrationStatus:
    if value in PROJECT_MIGRATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_MIGRATION_STATUS_VALUES!r}")
