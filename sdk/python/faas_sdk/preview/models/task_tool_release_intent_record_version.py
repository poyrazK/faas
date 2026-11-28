from typing import Literal

TaskToolReleaseIntentRecordVersion = Literal[1]

TASK_TOOL_RELEASE_INTENT_RECORD_VERSION_VALUES: set[TaskToolReleaseIntentRecordVersion] = {
    1,
}


def check_task_tool_release_intent_record_version(value: int) -> TaskToolReleaseIntentRecordVersion:
    if value in TASK_TOOL_RELEASE_INTENT_RECORD_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_INTENT_RECORD_VERSION_VALUES!r}")
