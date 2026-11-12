from typing import Literal

CreateProjectEnvironmentBranchRequestEgress = Literal["contained", "live", "simulated"]

CREATE_PROJECT_ENVIRONMENT_BRANCH_REQUEST_EGRESS_VALUES: set[CreateProjectEnvironmentBranchRequestEgress] = {
    "contained",
    "live",
    "simulated",
}


def check_create_project_environment_branch_request_egress(value: str) -> CreateProjectEnvironmentBranchRequestEgress:
    if value in CREATE_PROJECT_ENVIRONMENT_BRANCH_REQUEST_EGRESS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_PROJECT_ENVIRONMENT_BRANCH_REQUEST_EGRESS_VALUES!r}"
    )
