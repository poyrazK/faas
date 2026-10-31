from typing import Literal

WorkflowStepSpecMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

WORKFLOW_STEP_SPEC_METHOD_VALUES: set[WorkflowStepSpecMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_workflow_step_spec_method(value: str) -> WorkflowStepSpecMethod:
    if value in WORKFLOW_STEP_SPEC_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_STEP_SPEC_METHOD_VALUES!r}")
