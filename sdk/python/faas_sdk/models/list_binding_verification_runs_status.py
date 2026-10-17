from typing import Literal

ListBindingVerificationRunsStatus = Literal["cancelled", "expired", "passed", "pending"]

LIST_BINDING_VERIFICATION_RUNS_STATUS_VALUES: set[ListBindingVerificationRunsStatus] = {
    "cancelled",
    "expired",
    "passed",
    "pending",
}


def check_list_binding_verification_runs_status(value: str) -> ListBindingVerificationRunsStatus:
    if value in LIST_BINDING_VERIFICATION_RUNS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_BINDING_VERIFICATION_RUNS_STATUS_VALUES!r}")
