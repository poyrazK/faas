from typing import Literal

RouteMonitorCustomerReportGroupBy = Literal["consumer", "tenant"]

ROUTE_MONITOR_CUSTOMER_REPORT_GROUP_BY_VALUES: set[RouteMonitorCustomerReportGroupBy] = {
    "consumer",
    "tenant",
}


def check_route_monitor_customer_report_group_by(value: str) -> RouteMonitorCustomerReportGroupBy:
    if value in ROUTE_MONITOR_CUSTOMER_REPORT_GROUP_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_CUSTOMER_REPORT_GROUP_BY_VALUES!r}")
