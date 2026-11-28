from typing import Literal

BindingVerificationRunStatus = Literal["blocked", "cancelled", "expired", "passed", "pending"]

BINDING_VERIFICATION_RUN_STATUS_VALUES: set[BindingVerificationRunStatus] = {
    "blocked",
    "cancelled",
    "expired",
    "passed",
    "pending",
}


def check_binding_verification_run_status(value: str) -> BindingVerificationRunStatus:
    if value in BINDING_VERIFICATION_RUN_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_VERIFICATION_RUN_STATUS_VALUES!r}")
