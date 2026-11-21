from typing import Literal

TaskToolReleaseIntentAccessMode = Literal["external-oauth", "open"]

TASK_TOOL_RELEASE_INTENT_ACCESS_MODE_VALUES: set[TaskToolReleaseIntentAccessMode] = {
    "external-oauth",
    "open",
}


def check_task_tool_release_intent_access_mode(value: str) -> TaskToolReleaseIntentAccessMode:
    if value in TASK_TOOL_RELEASE_INTENT_ACCESS_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_INTENT_ACCESS_MODE_VALUES!r}")
