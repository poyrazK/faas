from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_evidence_customer_group_by import (
    RouteMonitorEvidenceCustomerGroupBy,
    check_route_monitor_evidence_customer_group_by,
)
from ..models.route_monitor_evidence_method import RouteMonitorEvidenceMethod, check_route_monitor_evidence_method
from ..models.route_monitor_evidence_signal import RouteMonitorEvidenceSignal, check_route_monitor_evidence_signal
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_evidence_window import RouteMonitorEvidenceWindow


T = TypeVar("T", bound="RouteMonitorEvidence")


@_attrs_define
class RouteMonitorEvidence:
    """One violated route and selected signal captured at incident opening or escalation. Customer-only opening violations
    are scoped to the affected request-time identity; escalation evidence is aggregate and contains no customer
    identity.

    """

    method: RouteMonitorEvidenceMethod
    path: str
    signal: RouteMonitorEvidenceSignal
    windows: list[RouteMonitorEvidenceWindow]
    customer_group_by: RouteMonitorEvidenceCustomerGroupBy | Unset = UNSET
    customer_id: UUID | Unset = UNSET
    """Affected request-time identity UUID; included only on explicitly opted-in reads."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        signal: str = self.signal

        windows = []
        for windows_item_data in self.windows:
            windows_item = windows_item_data.to_dict()
            windows.append(windows_item)

        customer_group_by: str | Unset = UNSET
        if not isinstance(self.customer_group_by, Unset):
            customer_group_by = self.customer_group_by

        customer_id: str | Unset = UNSET
        if not isinstance(self.customer_id, Unset):
            customer_id = str(self.customer_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "signal": signal,
                "windows": windows,
            }
        )
        if customer_group_by is not UNSET:
            field_dict["customer_group_by"] = customer_group_by
        if customer_id is not UNSET:
            field_dict["customer_id"] = customer_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_evidence_window import RouteMonitorEvidenceWindow

        d = dict(src_dict)
        method = check_route_monitor_evidence_method(d.pop("method"))

        path = d.pop("path")

        signal = check_route_monitor_evidence_signal(d.pop("signal"))

        windows = []
        _windows = d.pop("windows")
        for windows_item_data in _windows:
            windows_item = RouteMonitorEvidenceWindow.from_dict(windows_item_data)

            windows.append(windows_item)

        _customer_group_by = d.pop("customer_group_by", UNSET)
        customer_group_by: RouteMonitorEvidenceCustomerGroupBy | Unset
        if isinstance(_customer_group_by, Unset):
            customer_group_by = UNSET
        else:
            customer_group_by = check_route_monitor_evidence_customer_group_by(_customer_group_by)

        _customer_id = d.pop("customer_id", UNSET)
        customer_id: UUID | Unset
        if isinstance(_customer_id, Unset):
            customer_id = UNSET
        else:
            customer_id = UUID(_customer_id)

        route_monitor_evidence = cls(
            method=method,
            path=path,
            signal=signal,
            windows=windows,
            customer_group_by=customer_group_by,
            customer_id=customer_id,
        )

        route_monitor_evidence.additional_properties = d
        return route_monitor_evidence

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
