from typing import Literal

AutomationLiveSuiteCaseComparisonChange = Literal[
    "added", "incompatible", "recovery", "regression", "removed", "unchanged", "unknown"
]

AUTOMATION_LIVE_SUITE_CASE_COMPARISON_CHANGE_VALUES: set[AutomationLiveSuiteCaseComparisonChange] = {
    "added",
    "incompatible",
    "recovery",
    "regression",
    "removed",
    "unchanged",
    "unknown",
}


def check_automation_live_suite_case_comparison_change(value: str) -> AutomationLiveSuiteCaseComparisonChange:
    if value in AUTOMATION_LIVE_SUITE_CASE_COMPARISON_CHANGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {AUTOMATION_LIVE_SUITE_CASE_COMPARISON_CHANGE_VALUES!r}"
    )
