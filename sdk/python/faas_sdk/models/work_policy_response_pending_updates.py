from typing import Literal

WorkPolicyResponsePendingUpdates = Literal["all", "keep_latest"]

WORK_POLICY_RESPONSE_PENDING_UPDATES_VALUES: set[WorkPolicyResponsePendingUpdates] = {
    "all",
    "keep_latest",
}


def check_work_policy_response_pending_updates(value: str) -> WorkPolicyResponsePendingUpdates:
    if value in WORK_POLICY_RESPONSE_PENDING_UPDATES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORK_POLICY_RESPONSE_PENDING_UPDATES_VALUES!r}")
