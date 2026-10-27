from typing import Literal

RouteCustomerHealthReportGroupBy = Literal["consumer", "tenant"]

ROUTE_CUSTOMER_HEALTH_REPORT_GROUP_BY_VALUES: set[RouteCustomerHealthReportGroupBy] = {
    "consumer",
    "tenant",
}


def check_route_customer_health_report_group_by(value: str) -> RouteCustomerHealthReportGroupBy:
    if value in ROUTE_CUSTOMER_HEALTH_REPORT_GROUP_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_CUSTOMER_HEALTH_REPORT_GROUP_BY_VALUES!r}")
