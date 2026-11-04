from typing import Literal

TenantWorkflowScheduleResponseCatchUp = Literal["latest", "skip"]

TENANT_WORKFLOW_SCHEDULE_RESPONSE_CATCH_UP_VALUES: set[TenantWorkflowScheduleResponseCatchUp] = {
    "latest",
    "skip",
}


def check_tenant_workflow_schedule_response_catch_up(value: str) -> TenantWorkflowScheduleResponseCatchUp:
    if value in TENANT_WORKFLOW_SCHEDULE_RESPONSE_CATCH_UP_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TENANT_WORKFLOW_SCHEDULE_RESPONSE_CATCH_UP_VALUES!r}"
    )
