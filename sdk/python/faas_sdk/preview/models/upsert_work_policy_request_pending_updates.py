from typing import Literal

UpsertWorkPolicyRequestPendingUpdates = Literal["all", "keep_latest"]

UPSERT_WORK_POLICY_REQUEST_PENDING_UPDATES_VALUES: set[UpsertWorkPolicyRequestPendingUpdates] = {
    "all",
    "keep_latest",
}


def check_upsert_work_policy_request_pending_updates(value: str) -> UpsertWorkPolicyRequestPendingUpdates:
    if value in UPSERT_WORK_POLICY_REQUEST_PENDING_UPDATES_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPSERT_WORK_POLICY_REQUEST_PENDING_UPDATES_VALUES!r}"
    )
