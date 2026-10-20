from typing import Literal

SetAutomationPublishPolicyRequestMode = Literal["coverage", "optional", "scenarios"]

SET_AUTOMATION_PUBLISH_POLICY_REQUEST_MODE_VALUES: set[SetAutomationPublishPolicyRequestMode] = {
    "coverage",
    "optional",
    "scenarios",
}


def check_set_automation_publish_policy_request_mode(value: str) -> SetAutomationPublishPolicyRequestMode:
    if value in SET_AUTOMATION_PUBLISH_POLICY_REQUEST_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_AUTOMATION_PUBLISH_POLICY_REQUEST_MODE_VALUES!r}"
    )
