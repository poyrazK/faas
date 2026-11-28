from typing import Literal

AutomaticRouteCheckStaleReasonsItem = Literal["capture_changed", "configuration_changed", "requirements_changed"]

AUTOMATIC_ROUTE_CHECK_STALE_REASONS_ITEM_VALUES: set[AutomaticRouteCheckStaleReasonsItem] = {
    "capture_changed",
    "configuration_changed",
    "requirements_changed",
}


def check_automatic_route_check_stale_reasons_item(value: str) -> AutomaticRouteCheckStaleReasonsItem:
    if value in AUTOMATIC_ROUTE_CHECK_STALE_REASONS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATIC_ROUTE_CHECK_STALE_REASONS_ITEM_VALUES!r}")
