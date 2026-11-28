from typing import Literal

ListWorkflowSchedulesResponseUnavailableReason = Literal[
    "account_inactive", "maintenance", "plan_not_allowed", "runtime_disabled", "tenant_required"
]

LIST_WORKFLOW_SCHEDULES_RESPONSE_UNAVAILABLE_REASON_VALUES: set[ListWorkflowSchedulesResponseUnavailableReason] = {
    "account_inactive",
    "maintenance",
    "plan_not_allowed",
    "runtime_disabled",
    "tenant_required",
}


def check_list_workflow_schedules_response_unavailable_reason(
    value: str,
) -> ListWorkflowSchedulesResponseUnavailableReason:
    if value in LIST_WORKFLOW_SCHEDULES_RESPONSE_UNAVAILABLE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_WORKFLOW_SCHEDULES_RESPONSE_UNAVAILABLE_REASON_VALUES!r}"
    )
