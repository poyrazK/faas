from typing import Literal

RouteMonitorCustomerImpactGroupBy = Literal["consumer", "tenant"]

ROUTE_MONITOR_CUSTOMER_IMPACT_GROUP_BY_VALUES: set[RouteMonitorCustomerImpactGroupBy] = {
    "consumer",
    "tenant",
}


def check_route_monitor_customer_impact_group_by(value: str) -> RouteMonitorCustomerImpactGroupBy:
    if value in ROUTE_MONITOR_CUSTOMER_IMPACT_GROUP_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CUSTOMER_IMPACT_GROUP_BY_VALUES!r}")
