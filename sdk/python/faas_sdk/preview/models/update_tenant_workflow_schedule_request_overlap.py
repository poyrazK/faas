from typing import Literal

UpdateTenantWorkflowScheduleRequestOverlap = Literal["allow", "skip"]

UPDATE_TENANT_WORKFLOW_SCHEDULE_REQUEST_OVERLAP_VALUES: set[UpdateTenantWorkflowScheduleRequestOverlap] = {
    "allow",
    "skip",
}


def check_update_tenant_workflow_schedule_request_overlap(value: str) -> UpdateTenantWorkflowScheduleRequestOverlap:
    if value in UPDATE_TENANT_WORKFLOW_SCHEDULE_REQUEST_OVERLAP_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_TENANT_WORKFLOW_SCHEDULE_REQUEST_OVERLAP_VALUES!r}"
    )
