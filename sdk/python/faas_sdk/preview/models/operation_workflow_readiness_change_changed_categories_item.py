from typing import Literal

OperationWorkflowReadinessChangeChangedCategoriesItem = Literal[
    "blockers", "prerequisites", "reporting", "verification"
]

OPERATION_WORKFLOW_READINESS_CHANGE_CHANGED_CATEGORIES_ITEM_VALUES: set[
    OperationWorkflowReadinessChangeChangedCategoriesItem
] = {
    "blockers",
    "prerequisites",
    "reporting",
    "verification",
}


def check_operation_workflow_readiness_change_changed_categories_item(
    value: str,
) -> OperationWorkflowReadinessChangeChangedCategoriesItem:
    if value in OPERATION_WORKFLOW_READINESS_CHANGE_CHANGED_CATEGORIES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_CHANGE_CHANGED_CATEGORIES_ITEM_VALUES!r}"
    )
