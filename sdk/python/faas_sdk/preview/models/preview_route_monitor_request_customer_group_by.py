from typing import Literal

PreviewRouteMonitorRequestCustomerGroupBy = Literal["consumer", "tenant"]

PREVIEW_ROUTE_MONITOR_REQUEST_CUSTOMER_GROUP_BY_VALUES: set[PreviewRouteMonitorRequestCustomerGroupBy] = {
    "consumer",
    "tenant",
}


def check_preview_route_monitor_request_customer_group_by(value: str) -> PreviewRouteMonitorRequestCustomerGroupBy:
    if value in PREVIEW_ROUTE_MONITOR_REQUEST_CUSTOMER_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PREVIEW_ROUTE_MONITOR_REQUEST_CUSTOMER_GROUP_BY_VALUES!r}"
    )
