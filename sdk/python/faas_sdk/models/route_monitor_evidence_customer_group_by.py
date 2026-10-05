from typing import Literal

RouteMonitorEvidenceCustomerGroupBy = Literal["consumer", "tenant"]

ROUTE_MONITOR_EVIDENCE_CUSTOMER_GROUP_BY_VALUES: set[RouteMonitorEvidenceCustomerGroupBy] = {
    "consumer",
    "tenant",
}


def check_route_monitor_evidence_customer_group_by(value: str) -> RouteMonitorEvidenceCustomerGroupBy:
    if value in ROUTE_MONITOR_EVIDENCE_CUSTOMER_GROUP_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_EVIDENCE_CUSTOMER_GROUP_BY_VALUES!r}")
