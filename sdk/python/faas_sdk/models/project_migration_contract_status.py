from typing import Literal

ProjectMigrationContractStatus = Literal[
    "blocked",
    "cancelled",
    "failed",
    "queued",
    "ready",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
    "uncertain",
    "unknown",
]

PROJECT_MIGRATION_CONTRACT_STATUS_VALUES: set[ProjectMigrationContractStatus] = {
    "blocked",
    "cancelled",
    "failed",
    "queued",
    "ready",
    "restoring",
    "running",
    "succeeded",
    "timed_out",
    "uncertain",
    "unknown",
}


def check_project_migration_contract_status(value: str) -> ProjectMigrationContractStatus:
    if value in PROJECT_MIGRATION_CONTRACT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_MIGRATION_CONTRACT_STATUS_VALUES!r}")
