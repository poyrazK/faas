from typing import Literal

WorkflowScheduleCatchUpPreviewPolicy = Literal["latest", "skip"]

WORKFLOW_SCHEDULE_CATCH_UP_PREVIEW_POLICY_VALUES: set[WorkflowScheduleCatchUpPreviewPolicy] = {
    "latest",
    "skip",
}


def check_workflow_schedule_catch_up_preview_policy(value: str) -> WorkflowScheduleCatchUpPreviewPolicy:
    if value in WORKFLOW_SCHEDULE_CATCH_UP_PREVIEW_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_SCHEDULE_CATCH_UP_PREVIEW_POLICY_VALUES!r}")
