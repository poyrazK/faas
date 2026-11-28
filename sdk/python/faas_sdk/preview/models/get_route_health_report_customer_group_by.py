from typing import Literal

GetRouteHealthReportCustomerGroupBy = Literal["consumer", "tenant"]

GET_ROUTE_HEALTH_REPORT_CUSTOMER_GROUP_BY_VALUES: set[GetRouteHealthReportCustomerGroupBy] = {
    "consumer",
    "tenant",
}


def check_get_route_health_report_customer_group_by(value: str) -> GetRouteHealthReportCustomerGroupBy:
    if value in GET_ROUTE_HEALTH_REPORT_CUSTOMER_GROUP_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_ROUTE_HEALTH_REPORT_CUSTOMER_GROUP_BY_VALUES!r}")
