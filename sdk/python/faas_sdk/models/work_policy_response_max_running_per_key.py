from typing import Literal

WorkPolicyResponseMaxRunningPerKey = Literal[1]

WORK_POLICY_RESPONSE_MAX_RUNNING_PER_KEY_VALUES: set[WorkPolicyResponseMaxRunningPerKey] = {
    1,
}


def check_work_policy_response_max_running_per_key(value: int) -> WorkPolicyResponseMaxRunningPerKey:
    if value in WORK_POLICY_RESPONSE_MAX_RUNNING_PER_KEY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORK_POLICY_RESPONSE_MAX_RUNNING_PER_KEY_VALUES!r}")
