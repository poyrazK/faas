from typing import Literal

ObjectCapacityReconciliationInventoryScope = Literal["all_versions", "current"]

OBJECT_CAPACITY_RECONCILIATION_INVENTORY_SCOPE_VALUES: set[ObjectCapacityReconciliationInventoryScope] = {
    "all_versions",
    "current",
}


def check_object_capacity_reconciliation_inventory_scope(value: str) -> ObjectCapacityReconciliationInventoryScope:
    if value in OBJECT_CAPACITY_RECONCILIATION_INVENTORY_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBJECT_CAPACITY_RECONCILIATION_INVENTORY_SCOPE_VALUES!r}"
    )
