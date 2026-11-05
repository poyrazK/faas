from typing import Literal

RuntimePolicySchedulerStatusState = Literal["active", "pending", "unverified"]

RUNTIME_POLICY_SCHEDULER_STATUS_STATE_VALUES: set[RuntimePolicySchedulerStatusState] = {
    "active",
    "pending",
    "unverified",
}


def check_runtime_policy_scheduler_status_state(value: str) -> RuntimePolicySchedulerStatusState:
    if value in RUNTIME_POLICY_SCHEDULER_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUNTIME_POLICY_SCHEDULER_STATUS_STATE_VALUES!r}")
