from typing import Literal

ListAccountOperationsState = Literal[
    "accepted", "cancelled", "failed", "requires_reconciliation", "running", "succeeded", "waiting"
]

LIST_ACCOUNT_OPERATIONS_STATE_VALUES: set[ListAccountOperationsState] = {
    "accepted",
    "cancelled",
    "failed",
    "requires_reconciliation",
    "running",
    "succeeded",
    "waiting",
}


def check_list_account_operations_state(value: str) -> ListAccountOperationsState:
    if value in LIST_ACCOUNT_OPERATIONS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_OPERATIONS_STATE_VALUES!r}")
