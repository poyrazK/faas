from typing import Literal

RouteMonitorConfigCustomerGroupBy = Literal["consumer", "tenant"]

ROUTE_MONITOR_CONFIG_CUSTOMER_GROUP_BY_VALUES: set[RouteMonitorConfigCustomerGroupBy] = {
    "consumer",
    "tenant",
}


def check_route_monitor_config_customer_group_by(value: str) -> RouteMonitorConfigCustomerGroupBy:
    if value in ROUTE_MONITOR_CONFIG_CUSTOMER_GROUP_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CONFIG_CUSTOMER_GROUP_BY_VALUES!r}")
