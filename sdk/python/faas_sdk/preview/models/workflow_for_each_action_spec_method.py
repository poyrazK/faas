from typing import Literal

WorkflowForEachActionSpecMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

WORKFLOW_FOR_EACH_ACTION_SPEC_METHOD_VALUES: set[WorkflowForEachActionSpecMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_workflow_for_each_action_spec_method(value: str) -> WorkflowForEachActionSpecMethod:
    if value in WORKFLOW_FOR_EACH_ACTION_SPEC_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_FOR_EACH_ACTION_SPEC_METHOD_VALUES!r}")
