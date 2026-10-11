from typing import Literal

ListPlatformTenantSelfWorkflowRunsStatus = Literal[
    "awaiting_event", "dead", "failed", "pending", "running", "succeeded"
]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_RUNS_STATUS_VALUES: set[ListPlatformTenantSelfWorkflowRunsStatus] = {
    "awaiting_event",
    "dead",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_list_platform_tenant_self_workflow_runs_status(value: str) -> ListPlatformTenantSelfWorkflowRunsStatus:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_RUNS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_RUNS_STATUS_VALUES!r}"
    )
