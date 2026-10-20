from typing import Literal

UpsertWorkPolicyRequestMaxRunningPerKey = Literal[1]

UPSERT_WORK_POLICY_REQUEST_MAX_RUNNING_PER_KEY_VALUES: set[UpsertWorkPolicyRequestMaxRunningPerKey] = {
    1,
}


def check_upsert_work_policy_request_max_running_per_key(value: int) -> UpsertWorkPolicyRequestMaxRunningPerKey:
    if value in UPSERT_WORK_POLICY_REQUEST_MAX_RUNNING_PER_KEY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPSERT_WORK_POLICY_REQUEST_MAX_RUNNING_PER_KEY_VALUES!r}"
    )
