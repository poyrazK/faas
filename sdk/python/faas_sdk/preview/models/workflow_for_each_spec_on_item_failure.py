from typing import Literal

WorkflowForEachSpecOnItemFailure = Literal["continue"]

WORKFLOW_FOR_EACH_SPEC_ON_ITEM_FAILURE_VALUES: set[WorkflowForEachSpecOnItemFailure] = {
    "continue",
}


def check_workflow_for_each_spec_on_item_failure(value: str) -> WorkflowForEachSpecOnItemFailure:
    if value in WORKFLOW_FOR_EACH_SPEC_ON_ITEM_FAILURE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_FOR_EACH_SPEC_ON_ITEM_FAILURE_VALUES!r}")
