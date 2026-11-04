from typing import Literal

OperationWorkflowReadinessSnapshotEntryAdvisoriesItem = Literal[
    "condition_reporting_unknown",
    "condition_reports_missing",
    "condition_reports_overdue",
    "deadline_overdue",
    "state_stale",
    "verification_pending",
]

OPERATION_WORKFLOW_READINESS_SNAPSHOT_ENTRY_ADVISORIES_ITEM_VALUES: set[
    OperationWorkflowReadinessSnapshotEntryAdvisoriesItem
] = {
    "condition_reporting_unknown",
    "condition_reports_missing",
    "condition_reports_overdue",
    "deadline_overdue",
    "state_stale",
    "verification_pending",
}


def check_operation_workflow_readiness_snapshot_entry_advisories_item(
    value: str,
) -> OperationWorkflowReadinessSnapshotEntryAdvisoriesItem:
    if value in OPERATION_WORKFLOW_READINESS_SNAPSHOT_ENTRY_ADVISORIES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_SNAPSHOT_ENTRY_ADVISORIES_ITEM_VALUES!r}"
    )
