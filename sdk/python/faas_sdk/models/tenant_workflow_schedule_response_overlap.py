from typing import Literal

TenantWorkflowScheduleResponseOverlap = Literal["allow", "skip"]

TENANT_WORKFLOW_SCHEDULE_RESPONSE_OVERLAP_VALUES: set[TenantWorkflowScheduleResponseOverlap] = {
    "allow",
    "skip",
}


def check_tenant_workflow_schedule_response_overlap(value: str) -> TenantWorkflowScheduleResponseOverlap:
    if value in TENANT_WORKFLOW_SCHEDULE_RESPONSE_OVERLAP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TENANT_WORKFLOW_SCHEDULE_RESPONSE_OVERLAP_VALUES!r}")
