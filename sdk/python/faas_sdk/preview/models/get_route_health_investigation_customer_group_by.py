from typing import Literal

GetRouteHealthInvestigationCustomerGroupBy = Literal["consumer", "tenant"]

GET_ROUTE_HEALTH_INVESTIGATION_CUSTOMER_GROUP_BY_VALUES: set[GetRouteHealthInvestigationCustomerGroupBy] = {
    "consumer",
    "tenant",
}


def check_get_route_health_investigation_customer_group_by(value: str) -> GetRouteHealthInvestigationCustomerGroupBy:
    if value in GET_ROUTE_HEALTH_INVESTIGATION_CUSTOMER_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GET_ROUTE_HEALTH_INVESTIGATION_CUSTOMER_GROUP_BY_VALUES!r}"
    )
