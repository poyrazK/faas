from typing import Literal

HostedTaskBindingResultStatus = Literal["failed", "not_checked", "passed"]

HOSTED_TASK_BINDING_RESULT_STATUS_VALUES: set[HostedTaskBindingResultStatus] = {
    "failed",
    "not_checked",
    "passed",
}


def check_hosted_task_binding_result_status(value: str) -> HostedTaskBindingResultStatus:
    if value in HOSTED_TASK_BINDING_RESULT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_BINDING_RESULT_STATUS_VALUES!r}")
