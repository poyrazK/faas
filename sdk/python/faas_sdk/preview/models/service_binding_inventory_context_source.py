from typing import Literal

ServiceBindingInventoryContextSource = Literal["environment_release"]

SERVICE_BINDING_INVENTORY_CONTEXT_SOURCE_VALUES: set[ServiceBindingInventoryContextSource] = {
    "environment_release",
}


def check_service_binding_inventory_context_source(value: str) -> ServiceBindingInventoryContextSource:
    if value in SERVICE_BINDING_INVENTORY_CONTEXT_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_BINDING_INVENTORY_CONTEXT_SOURCE_VALUES!r}")
