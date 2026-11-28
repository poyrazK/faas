from typing import Literal

RuntimePolicySchedulerStatusScope = Literal["app"]

RUNTIME_POLICY_SCHEDULER_STATUS_SCOPE_VALUES: set[RuntimePolicySchedulerStatusScope] = {
    "app",
}


def check_runtime_policy_scheduler_status_scope(value: str) -> RuntimePolicySchedulerStatusScope:
    if value in RUNTIME_POLICY_SCHEDULER_STATUS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_POLICY_SCHEDULER_STATUS_SCOPE_VALUES!r}")
