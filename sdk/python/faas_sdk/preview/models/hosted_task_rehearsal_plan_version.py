from typing import Literal

HostedTaskRehearsalPlanVersion = Literal[1]

HOSTED_TASK_REHEARSAL_PLAN_VERSION_VALUES: set[HostedTaskRehearsalPlanVersion] = {
    1,
}


def check_hosted_task_rehearsal_plan_version(value: int) -> HostedTaskRehearsalPlanVersion:
    if value in HOSTED_TASK_REHEARSAL_PLAN_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_REHEARSAL_PLAN_VERSION_VALUES!r}")
