from typing import Literal

ProjectEnvironmentBranchBucketConsistency = Literal["as_of", "live_parent"]

PROJECT_ENVIRONMENT_BRANCH_BUCKET_CONSISTENCY_VALUES: set[ProjectEnvironmentBranchBucketConsistency] = {
    "as_of",
    "live_parent",
}


def check_project_environment_branch_bucket_consistency(value: str) -> ProjectEnvironmentBranchBucketConsistency:
    if value in PROJECT_ENVIRONMENT_BRANCH_BUCKET_CONSISTENCY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_BRANCH_BUCKET_CONSISTENCY_VALUES!r}"
    )
