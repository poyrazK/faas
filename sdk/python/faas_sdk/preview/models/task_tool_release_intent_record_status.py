from typing import Literal

TaskToolReleaseIntentRecordStatus = Literal["approved", "expired", "pending", "rejected", "revoked", "stale"]

TASK_TOOL_RELEASE_INTENT_RECORD_STATUS_VALUES: set[TaskToolReleaseIntentRecordStatus] = {
    "approved",
    "expired",
    "pending",
    "rejected",
    "revoked",
    "stale",
}


def check_task_tool_release_intent_record_status(value: str) -> TaskToolReleaseIntentRecordStatus:
    if value in TASK_TOOL_RELEASE_INTENT_RECORD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_INTENT_RECORD_STATUS_VALUES!r}")
