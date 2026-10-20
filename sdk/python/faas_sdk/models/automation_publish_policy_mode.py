from typing import Literal

AutomationPublishPolicyMode = Literal["coverage", "optional", "scenarios"]

AUTOMATION_PUBLISH_POLICY_MODE_VALUES: set[AutomationPublishPolicyMode] = {
    "coverage",
    "optional",
    "scenarios",
}


def check_automation_publish_policy_mode(value: str) -> AutomationPublishPolicyMode:
    if value in AUTOMATION_PUBLISH_POLICY_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_PUBLISH_POLICY_MODE_VALUES!r}")
