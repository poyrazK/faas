from typing import Literal

TaskToolReleaseIntentProtocol = Literal["2026-07-28"]

TASK_TOOL_RELEASE_INTENT_PROTOCOL_VALUES: set[TaskToolReleaseIntentProtocol] = {
    "2026-07-28",
}


def check_task_tool_release_intent_protocol(value: str) -> TaskToolReleaseIntentProtocol:
    if value in TASK_TOOL_RELEASE_INTENT_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_TOOL_RELEASE_INTENT_PROTOCOL_VALUES!r}")
