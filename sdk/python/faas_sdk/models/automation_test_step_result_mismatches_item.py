from typing import Literal

AutomationTestStepResultMismatchesItem = Literal[
    "attempt_count", "attempts", "output", "reason", "state", "step", "when_matched"
]

AUTOMATION_TEST_STEP_RESULT_MISMATCHES_ITEM_VALUES: set[AutomationTestStepResultMismatchesItem] = {
    "attempt_count",
    "attempts",
    "output",
    "reason",
    "state",
    "step",
    "when_matched",
}


def check_automation_test_step_result_mismatches_item(value: str) -> AutomationTestStepResultMismatchesItem:
    if value in AUTOMATION_TEST_STEP_RESULT_MISMATCHES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {AUTOMATION_TEST_STEP_RESULT_MISMATCHES_ITEM_VALUES!r}"
    )
