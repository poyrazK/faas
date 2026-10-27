from typing import Literal

RouteHealthInvestigationSelectionCustomerGroupBy = Literal["consumer", "tenant"]

ROUTE_HEALTH_INVESTIGATION_SELECTION_CUSTOMER_GROUP_BY_VALUES: set[RouteHealthInvestigationSelectionCustomerGroupBy] = {
    "consumer",
    "tenant",
}


def check_route_health_investigation_selection_customer_group_by(
    value: str,
) -> RouteHealthInvestigationSelectionCustomerGroupBy:
    if value in ROUTE_HEALTH_INVESTIGATION_SELECTION_CUSTOMER_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_HEALTH_INVESTIGATION_SELECTION_CUSTOMER_GROUP_BY_VALUES!r}"
    )
