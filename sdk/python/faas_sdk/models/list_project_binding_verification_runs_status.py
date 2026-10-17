from typing import Literal

ListProjectBindingVerificationRunsStatus = Literal["cancelled", "expired", "passed", "pending"]

LIST_PROJECT_BINDING_VERIFICATION_RUNS_STATUS_VALUES: set[ListProjectBindingVerificationRunsStatus] = {
    "cancelled",
    "expired",
    "passed",
    "pending",
}


def check_list_project_binding_verification_runs_status(value: str) -> ListProjectBindingVerificationRunsStatus:
    if value in LIST_PROJECT_BINDING_VERIFICATION_RUNS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PROJECT_BINDING_VERIFICATION_RUNS_STATUS_VALUES!r}"
    )
