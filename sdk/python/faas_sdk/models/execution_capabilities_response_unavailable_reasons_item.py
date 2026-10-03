from typing import Literal

ExecutionCapabilitiesResponseUnavailableReasonsItem = Literal["control_plane_disabled", "plan_not_entitled"]

EXECUTION_CAPABILITIES_RESPONSE_UNAVAILABLE_REASONS_ITEM_VALUES: set[
    ExecutionCapabilitiesResponseUnavailableReasonsItem
] = {
    "control_plane_disabled",
    "plan_not_entitled",
}


def check_execution_capabilities_response_unavailable_reasons_item(
    value: str,
) -> ExecutionCapabilitiesResponseUnavailableReasonsItem:
    if value in EXECUTION_CAPABILITIES_RESPONSE_UNAVAILABLE_REASONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EXECUTION_CAPABILITIES_RESPONSE_UNAVAILABLE_REASONS_ITEM_VALUES!r}"
    )
