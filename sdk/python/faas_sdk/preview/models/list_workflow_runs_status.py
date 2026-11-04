from typing import Literal

ListWorkflowRunsStatus = Literal["awaiting_event", "dead", "failed", "pending", "running", "succeeded"]

LIST_WORKFLOW_RUNS_STATUS_VALUES: set[ListWorkflowRunsStatus] = {
    "awaiting_event",
    "dead",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_list_workflow_runs_status(value: str) -> ListWorkflowRunsStatus:
    if value in LIST_WORKFLOW_RUNS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_WORKFLOW_RUNS_STATUS_VALUES!r}")
