from typing import Literal

ServiceBindingInventoryContextRoutingStatus = Literal["denied", "resolved", "unavailable"]

SERVICE_BINDING_INVENTORY_CONTEXT_ROUTING_STATUS_VALUES: set[ServiceBindingInventoryContextRoutingStatus] = {
    "denied",
    "resolved",
    "unavailable",
}


def check_service_binding_inventory_context_routing_status(value: str) -> ServiceBindingInventoryContextRoutingStatus:
    if value in SERVICE_BINDING_INVENTORY_CONTEXT_ROUTING_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SERVICE_BINDING_INVENTORY_CONTEXT_ROUTING_STATUS_VALUES!r}"
    )
