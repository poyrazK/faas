from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_customer_cohort_error_status import (
    RouteMonitorCustomerCohortErrorStatus,
    check_route_monitor_customer_cohort_error_status,
)
from ..models.route_monitor_customer_cohort_latency_status import (
    RouteMonitorCustomerCohortLatencyStatus,
    check_route_monitor_customer_cohort_latency_status,
)
from ..models.route_monitor_customer_cohort_status import (
    RouteMonitorCustomerCohortStatus,
    check_route_monitor_customer_cohort_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_window import RouteMonitorWindow


T = TypeVar("T", bound="RouteMonitorCustomerCohort")


@_attrs_define
class RouteMonitorCustomerCohort:
    """One bounded request-time customer cohort; its UUID is returned only when customer_details=true."""

    observed: bool
    status: RouteMonitorCustomerCohortStatus
    reason: str
    error_status: RouteMonitorCustomerCohortErrorStatus
    latency_status: RouteMonitorCustomerCohortLatencyStatus
    windows: list[RouteMonitorWindow]
    customer_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed = self.observed

        status: str = self.status

        reason = self.reason

        error_status: str = self.error_status

        latency_status: str = self.latency_status

        windows = []
        for windows_item_data in self.windows:
            windows_item = windows_item_data.to_dict()
            windows.append(windows_item)

        customer_id: str | Unset = UNSET
        if not isinstance(self.customer_id, Unset):
            customer_id = str(self.customer_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed": observed,
                "status": status,
                "reason": reason,
                "error_status": error_status,
                "latency_status": latency_status,
                "windows": windows,
            }
        )
        if customer_id is not UNSET:
            field_dict["customer_id"] = customer_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_window import RouteMonitorWindow

        d = dict(src_dict)
        observed = d.pop("observed")

        status = check_route_monitor_customer_cohort_status(d.pop("status"))

        reason = d.pop("reason")

        error_status = check_route_monitor_customer_cohort_error_status(d.pop("error_status"))

        latency_status = check_route_monitor_customer_cohort_latency_status(d.pop("latency_status"))

        windows = []
        _windows = d.pop("windows")
        for windows_item_data in _windows:
            windows_item = RouteMonitorWindow.from_dict(windows_item_data)

            windows.append(windows_item)

        _customer_id = d.pop("customer_id", UNSET)
        customer_id: UUID | Unset
        if isinstance(_customer_id, Unset):
            customer_id = UNSET
        else:
            customer_id = UUID(_customer_id)

        route_monitor_customer_cohort = cls(
            observed=observed,
            status=status,
            reason=reason,
            error_status=error_status,
            latency_status=latency_status,
            windows=windows,
            customer_id=customer_id,
        )

        route_monitor_customer_cohort.additional_properties = d
        return route_monitor_customer_cohort

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
