from typing import Literal

RouteMonitorReportCustomerGroupBy = Literal["consumer", "tenant"]

ROUTE_MONITOR_REPORT_CUSTOMER_GROUP_BY_VALUES: set[RouteMonitorReportCustomerGroupBy] = {
    "consumer",
    "tenant",
}


def check_route_monitor_report_customer_group_by(value: str) -> RouteMonitorReportCustomerGroupBy:
    if value in ROUTE_MONITOR_REPORT_CUSTOMER_GROUP_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_REPORT_CUSTOMER_GROUP_BY_VALUES!r}")
