from typing import Literal

BindingInventoryIssueSeverity = Literal["error", "warning"]

BINDING_INVENTORY_ISSUE_SEVERITY_VALUES: set[BindingInventoryIssueSeverity] = {
    "error",
    "warning",
}


def check_binding_inventory_issue_severity(value: str) -> BindingInventoryIssueSeverity:
    if value in BINDING_INVENTORY_ISSUE_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_INVENTORY_ISSUE_SEVERITY_VALUES!r}")
