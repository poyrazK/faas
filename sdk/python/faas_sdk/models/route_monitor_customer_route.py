from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_customer_route_method import (
    RouteMonitorCustomerRouteMethod,
    check_route_monitor_customer_route_method,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_customer_cohort import RouteMonitorCustomerCohort
    from ..models.route_monitor_customer_window import RouteMonitorCustomerWindow


T = TypeVar("T", bound="RouteMonitorCustomerRoute")


@_attrs_define
class RouteMonitorCustomerRoute:
    """Full-population route counts plus at most five displayed cohorts and one hundred violating identities."""

    method: RouteMonitorCustomerRouteMethod
    path: str
    observed_customers: int
    violated_customers: int
    unknown_customers: int
    recovery_missing_customers: int
    recovery_remaining_customers: int
    customers_truncated: bool
    violating_customers_truncated: bool
    windows: list[RouteMonitorCustomerWindow]
    customers: list[RouteMonitorCustomerCohort]
    violating_customer_ids: list[UUID] | Unset = UNSET
    """Present only when customer_details=true."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        observed_customers = self.observed_customers

        violated_customers = self.violated_customers

        unknown_customers = self.unknown_customers

        recovery_missing_customers = self.recovery_missing_customers

        recovery_remaining_customers = self.recovery_remaining_customers

        customers_truncated = self.customers_truncated

        violating_customers_truncated = self.violating_customers_truncated

        windows = []
        for windows_item_data in self.windows:
            windows_item = windows_item_data.to_dict()
            windows.append(windows_item)

        customers = []
        for customers_item_data in self.customers:
            customers_item = customers_item_data.to_dict()
            customers.append(customers_item)

        violating_customer_ids: list[str] | Unset = UNSET
        if not isinstance(self.violating_customer_ids, Unset):
            violating_customer_ids = []
            for violating_customer_ids_item_data in self.violating_customer_ids:
                violating_customer_ids_item = str(violating_customer_ids_item_data)
                violating_customer_ids.append(violating_customer_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "observed_customers": observed_customers,
                "violated_customers": violated_customers,
                "unknown_customers": unknown_customers,
                "recovery_missing_customers": recovery_missing_customers,
                "recovery_remaining_customers": recovery_remaining_customers,
                "customers_truncated": customers_truncated,
                "violating_customers_truncated": violating_customers_truncated,
                "windows": windows,
                "customers": customers,
            }
        )
        if violating_customer_ids is not UNSET:
            field_dict["violating_customer_ids"] = violating_customer_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_customer_cohort import RouteMonitorCustomerCohort
        from ..models.route_monitor_customer_window import RouteMonitorCustomerWindow

        d = dict(src_dict)
        method = check_route_monitor_customer_route_method(d.pop("method"))

        path = d.pop("path")

        observed_customers = d.pop("observed_customers")

        violated_customers = d.pop("violated_customers")

        unknown_customers = d.pop("unknown_customers")

        recovery_missing_customers = d.pop("recovery_missing_customers")

        recovery_remaining_customers = d.pop("recovery_remaining_customers")

        customers_truncated = d.pop("customers_truncated")

        violating_customers_truncated = d.pop("violating_customers_truncated")

        windows = []
        _windows = d.pop("windows")
        for windows_item_data in _windows:
            windows_item = RouteMonitorCustomerWindow.from_dict(windows_item_data)

            windows.append(windows_item)

        customers = []
        _customers = d.pop("customers")
        for customers_item_data in _customers:
            customers_item = RouteMonitorCustomerCohort.from_dict(customers_item_data)

            customers.append(customers_item)

        _violating_customer_ids = d.pop("violating_customer_ids", UNSET)
        violating_customer_ids: list[UUID] | Unset = UNSET
        if _violating_customer_ids is not UNSET:
            violating_customer_ids = []
            for violating_customer_ids_item_data in _violating_customer_ids:
                violating_customer_ids_item = UUID(violating_customer_ids_item_data)

                violating_customer_ids.append(violating_customer_ids_item)

        route_monitor_customer_route = cls(
            method=method,
            path=path,
            observed_customers=observed_customers,
            violated_customers=violated_customers,
            unknown_customers=unknown_customers,
            recovery_missing_customers=recovery_missing_customers,
            recovery_remaining_customers=recovery_remaining_customers,
            customers_truncated=customers_truncated,
            violating_customers_truncated=violating_customers_truncated,
            windows=windows,
            customers=customers,
            violating_customer_ids=violating_customer_ids,
        )

        route_monitor_customer_route.additional_properties = d
        return route_monitor_customer_route

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
