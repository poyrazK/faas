from typing import Literal

AutomationCheckExclusionCode = Literal[
    "failure_route_missing",
    "guard_match_missing",
    "guard_skip_missing",
    "loop_empty_missing",
    "loop_multiple_items_missing",
    "retry_missing",
    "wait_success_missing",
    "wait_timeout_missing",
]

AUTOMATION_CHECK_EXCLUSION_CODE_VALUES: set[AutomationCheckExclusionCode] = {
    "failure_route_missing",
    "guard_match_missing",
    "guard_skip_missing",
    "loop_empty_missing",
    "loop_multiple_items_missing",
    "retry_missing",
    "wait_success_missing",
    "wait_timeout_missing",
}


def check_automation_check_exclusion_code(value: str) -> AutomationCheckExclusionCode:
    if value in AUTOMATION_CHECK_EXCLUSION_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_CHECK_EXCLUSION_CODE_VALUES!r}")
