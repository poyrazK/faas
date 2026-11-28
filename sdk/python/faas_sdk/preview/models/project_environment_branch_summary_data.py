from typing import Literal

ProjectEnvironmentBranchSummaryData = Literal["failed", "pending", "ready"]

PROJECT_ENVIRONMENT_BRANCH_SUMMARY_DATA_VALUES: set[ProjectEnvironmentBranchSummaryData] = {
    "failed",
    "pending",
    "ready",
}


def check_project_environment_branch_summary_data(value: str) -> ProjectEnvironmentBranchSummaryData:
    if value in PROJECT_ENVIRONMENT_BRANCH_SUMMARY_DATA_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_BRANCH_SUMMARY_DATA_VALUES!r}")
