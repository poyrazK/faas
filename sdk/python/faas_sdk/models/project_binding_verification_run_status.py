from typing import Literal

ProjectBindingVerificationRunStatus = Literal["blocked", "cancelled", "expired", "passed", "pending"]

PROJECT_BINDING_VERIFICATION_RUN_STATUS_VALUES: set[ProjectBindingVerificationRunStatus] = {
    "blocked",
    "cancelled",
    "expired",
    "passed",
    "pending",
}


def check_project_binding_verification_run_status(value: str) -> ProjectBindingVerificationRunStatus:
    if value in PROJECT_BINDING_VERIFICATION_RUN_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_BINDING_VERIFICATION_RUN_STATUS_VALUES!r}")
