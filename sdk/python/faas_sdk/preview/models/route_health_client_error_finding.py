from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_client_error_finding_status import (
    RouteHealthClientErrorFindingStatus,
    check_route_health_client_error_finding_status,
)
from ..models.route_health_client_error_finding_status_code import (
    RouteHealthClientErrorFindingStatusCode,
    check_route_health_client_error_finding_status_code,
)

if TYPE_CHECKING:
    from ..models.route_health_client_error_window import RouteHealthClientErrorWindow


T = TypeVar("T", bound="RouteHealthClientErrorFinding")


@_attrs_define
class RouteHealthClientErrorFinding:
    """Independent consecutive-window verdict for one watched status code. Different codes in different windows cannot
    confirm a sustained regression.

    """

    status_code: RouteHealthClientErrorFindingStatusCode
    status: RouteHealthClientErrorFindingStatus
    reason: str
    windows: list[RouteHealthClientErrorWindow]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status_code: int = self.status_code

        status: str = self.status

        reason = self.reason

        windows = []
        for windows_item_data in self.windows:
            windows_item = windows_item_data.to_dict()
            windows.append(windows_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status_code": status_code,
                "status": status,
                "reason": reason,
                "windows": windows,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_client_error_window import RouteHealthClientErrorWindow

        d = dict(src_dict)
        status_code = check_route_health_client_error_finding_status_code(d.pop("status_code"))

        status = check_route_health_client_error_finding_status(d.pop("status"))

        reason = d.pop("reason")

        windows = []
        _windows = d.pop("windows")
        for windows_item_data in _windows:
            windows_item = RouteHealthClientErrorWindow.from_dict(windows_item_data)

            windows.append(windows_item)

        route_health_client_error_finding = cls(
            status_code=status_code,
            status=status,
            reason=reason,
            windows=windows,
        )

        route_health_client_error_finding.additional_properties = d
        return route_health_client_error_finding

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
