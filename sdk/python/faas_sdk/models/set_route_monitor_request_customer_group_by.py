from typing import Literal

SetRouteMonitorRequestCustomerGroupBy = Literal["consumer", "tenant"]

SET_ROUTE_MONITOR_REQUEST_CUSTOMER_GROUP_BY_VALUES: set[SetRouteMonitorRequestCustomerGroupBy] = {
    "consumer",
    "tenant",
}


def check_set_route_monitor_request_customer_group_by(value: str) -> SetRouteMonitorRequestCustomerGroupBy:
    if value in SET_ROUTE_MONITOR_REQUEST_CUSTOMER_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_ROUTE_MONITOR_REQUEST_CUSTOMER_GROUP_BY_VALUES!r}"
    )
