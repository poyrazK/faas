from typing import Literal

ProjectEnvironmentBranchResponseData = Literal["failed", "pending", "ready"]

PROJECT_ENVIRONMENT_BRANCH_RESPONSE_DATA_VALUES: set[ProjectEnvironmentBranchResponseData] = {
    "failed",
    "pending",
    "ready",
}


def check_project_environment_branch_response_data(value: str) -> ProjectEnvironmentBranchResponseData:
    if value in PROJECT_ENVIRONMENT_BRANCH_RESPONSE_DATA_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_BRANCH_RESPONSE_DATA_VALUES!r}")
