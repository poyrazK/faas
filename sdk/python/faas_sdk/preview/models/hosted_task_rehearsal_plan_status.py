from typing import Literal

HostedTaskRehearsalPlanStatus = Literal["approved", "claimed", "expired", "planned", "revoked", "stale"]

HOSTED_TASK_REHEARSAL_PLAN_STATUS_VALUES: set[HostedTaskRehearsalPlanStatus] = {
    "approved",
    "claimed",
    "expired",
    "planned",
    "revoked",
    "stale",
}


def check_hosted_task_rehearsal_plan_status(value: str) -> HostedTaskRehearsalPlanStatus:
    if value in HOSTED_TASK_REHEARSAL_PLAN_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_REHEARSAL_PLAN_STATUS_VALUES!r}")
