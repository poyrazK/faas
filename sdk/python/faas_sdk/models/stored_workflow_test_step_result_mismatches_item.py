from typing import Literal

StoredWorkflowTestStepResultMismatchesItem = Literal[
    "attempt_count", "attempts", "output", "reason", "state", "step", "when_matched"
]

STORED_WORKFLOW_TEST_STEP_RESULT_MISMATCHES_ITEM_VALUES: set[StoredWorkflowTestStepResultMismatchesItem] = {
    "attempt_count",
    "attempts",
    "output",
    "reason",
    "state",
    "step",
    "when_matched",
}


def check_stored_workflow_test_step_result_mismatches_item(value: str) -> StoredWorkflowTestStepResultMismatchesItem:
    if value in STORED_WORKFLOW_TEST_STEP_RESULT_MISMATCHES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_TEST_STEP_RESULT_MISMATCHES_ITEM_VALUES!r}"
    )
