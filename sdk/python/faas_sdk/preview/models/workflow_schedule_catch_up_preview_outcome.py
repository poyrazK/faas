from typing import Literal

WorkflowScheduleCatchUpPreviewOutcome = Literal[
    "coalesce_latest",
    "disabled",
    "first_evaluation_arms",
    "no_due_occurrence",
    "no_new_evaluation",
    "outside_catch_up_window",
    "run_current_fire",
    "skip_missed",
]

WORKFLOW_SCHEDULE_CATCH_UP_PREVIEW_OUTCOME_VALUES: set[WorkflowScheduleCatchUpPreviewOutcome] = {
    "coalesce_latest",
    "disabled",
    "first_evaluation_arms",
    "no_due_occurrence",
    "no_new_evaluation",
    "outside_catch_up_window",
    "run_current_fire",
    "skip_missed",
}


def check_workflow_schedule_catch_up_preview_outcome(value: str) -> WorkflowScheduleCatchUpPreviewOutcome:
    if value in WORKFLOW_SCHEDULE_CATCH_UP_PREVIEW_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_CATCH_UP_PREVIEW_OUTCOME_VALUES!r}"
    )
