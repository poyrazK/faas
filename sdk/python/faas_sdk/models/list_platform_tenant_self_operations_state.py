from typing import Literal

ListPlatformTenantSelfOperationsState = Literal[
    "accepted", "cancelled", "failed", "requires_reconciliation", "running", "succeeded"
]

LIST_PLATFORM_TENANT_SELF_OPERATIONS_STATE_VALUES: set[ListPlatformTenantSelfOperationsState] = {
    "accepted",
    "cancelled",
    "failed",
    "requires_reconciliation",
    "running",
    "succeeded",
}


def check_list_platform_tenant_self_operations_state(value: str) -> ListPlatformTenantSelfOperationsState:
    if value in LIST_PLATFORM_TENANT_SELF_OPERATIONS_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_OPERATIONS_STATE_VALUES!r}"
    )
