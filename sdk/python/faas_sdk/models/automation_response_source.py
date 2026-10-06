from typing import Literal

AutomationResponseSource = Literal["dashboard", "draft", "manifest"]

AUTOMATION_RESPONSE_SOURCE_VALUES: set[AutomationResponseSource] = {
    "dashboard",
    "draft",
    "manifest",
}


def check_automation_response_source(value: str) -> AutomationResponseSource:
    if value in AUTOMATION_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_RESPONSE_SOURCE_VALUES!r}")
