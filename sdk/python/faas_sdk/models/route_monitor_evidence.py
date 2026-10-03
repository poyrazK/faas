from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_evidence_method import RouteMonitorEvidenceMethod, check_route_monitor_evidence_method
from ..models.route_monitor_evidence_signal import RouteMonitorEvidenceSignal, check_route_monitor_evidence_signal

if TYPE_CHECKING:
    from ..models.route_monitor_evidence_window import RouteMonitorEvidenceWindow


T = TypeVar("T", bound="RouteMonitorEvidence")


@_attrs_define
class RouteMonitorEvidence:
    """One violated route and selected signal captured in the opening evaluation. At most three route/signal entries are
    saved in configuration order, errors before latency.

    """

    method: RouteMonitorEvidenceMethod
    path: str
    signal: RouteMonitorEvidenceSignal
    windows: list[RouteMonitorEvidenceWindow]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        signal: str = self.signal

        windows = []
        for windows_item_data in self.windows:
            windows_item = windows_item_data.to_dict()
            windows.append(windows_item)

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

        route_monitor_evidence = cls(
            method=method,
            path=path,
            signal=signal,
            windows=windows,
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
