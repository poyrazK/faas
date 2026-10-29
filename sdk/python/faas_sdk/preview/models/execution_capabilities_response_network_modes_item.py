from typing import Literal

ExecutionCapabilitiesResponseNetworkModesItem = Literal["none"]

EXECUTION_CAPABILITIES_RESPONSE_NETWORK_MODES_ITEM_VALUES: set[ExecutionCapabilitiesResponseNetworkModesItem] = {
    "none",
}


def check_execution_capabilities_response_network_modes_item(
    value: str,
) -> ExecutionCapabilitiesResponseNetworkModesItem:
    if value in EXECUTION_CAPABILITIES_RESPONSE_NETWORK_MODES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_CAPABILITIES_RESPONSE_NETWORK_MODES_ITEM_VALUES!r}"
    )
