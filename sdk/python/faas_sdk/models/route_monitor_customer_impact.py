from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_customer_impact_coverage import (
    RouteMonitorCustomerImpactCoverage,
    check_route_monitor_customer_impact_coverage,
)
from ..models.route_monitor_customer_impact_group_by import (
    RouteMonitorCustomerImpactGroupBy,
    check_route_monitor_customer_impact_group_by,
)

T = TypeVar("T", bound="RouteMonitorCustomerImpact")


@_attrs_define
class RouteMonitorCustomerImpact:
    """Aggregate observed request-time identity counts; never includes customer IDs."""

    group_by: RouteMonitorCustomerImpactGroupBy
    coverage: RouteMonitorCustomerImpactCoverage
    observed_customers: int
    violated_customers: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        group_by: str = self.group_by

        coverage: str = self.coverage

        observed_customers = self.observed_customers

        violated_customers = self.violated_customers

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "group_by": group_by,
                "coverage": coverage,
                "observed_customers": observed_customers,
                "violated_customers": violated_customers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        group_by = check_route_monitor_customer_impact_group_by(d.pop("group_by"))

        coverage = check_route_monitor_customer_impact_coverage(d.pop("coverage"))

        observed_customers = d.pop("observed_customers")

        violated_customers = d.pop("violated_customers")

        route_monitor_customer_impact = cls(
            group_by=group_by,
            coverage=coverage,
            observed_customers=observed_customers,
            violated_customers=violated_customers,
        )

        route_monitor_customer_impact.additional_properties = d
        return route_monitor_customer_impact

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
