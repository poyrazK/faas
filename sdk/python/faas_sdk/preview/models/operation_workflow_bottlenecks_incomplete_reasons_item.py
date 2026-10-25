from typing import Literal

OperationWorkflowBottlenecksIncompleteReasonsItem = Literal[
    "contract_changed",
    "duplicate_revision",
    "future_observation",
    "history_window_truncated",
    "missing_latest",
    "missing_start",
    "out_of_order_time",
    "revision_gap",
    "state_discontinuity",
    "verification_start_missing",
]

OPERATION_WORKFLOW_BOTTLENECKS_INCOMPLETE_REASONS_ITEM_VALUES: set[
    OperationWorkflowBottlenecksIncompleteReasonsItem
] = {
    "contract_changed",
    "duplicate_revision",
    "future_observation",
    "history_window_truncated",
    "missing_latest",
    "missing_start",
    "out_of_order_time",
    "revision_gap",
    "state_discontinuity",
    "verification_start_missing",
}


def check_operation_workflow_bottlenecks_incomplete_reasons_item(
    value: str,
) -> OperationWorkflowBottlenecksIncompleteReasonsItem:
    if value in OPERATION_WORKFLOW_BOTTLENECKS_INCOMPLETE_REASONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_BOTTLENECKS_INCOMPLETE_REASONS_ITEM_VALUES!r}"
    )
