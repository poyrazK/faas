from typing import Literal

AutomationLiveBaselinePolicyVersion = Literal[2]

AUTOMATION_LIVE_BASELINE_POLICY_VERSION_VALUES: set[AutomationLiveBaselinePolicyVersion] = {
    2,
}


def check_automation_live_baseline_policy_version(value: int) -> AutomationLiveBaselinePolicyVersion:
    if value in AUTOMATION_LIVE_BASELINE_POLICY_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_BASELINE_POLICY_VERSION_VALUES!r}")
