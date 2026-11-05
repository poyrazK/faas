from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_policy_affected_operation import RoutePolicyAffectedOperation


T = TypeVar("T", bound="RoutePolicyImpact")


@_attrs_define
class RoutePolicyImpact:
    """Platform host, method and concrete or family path whose policy selection can change. Captured impact is bounded to
    the selected contract; wildcard rules can also affect uncaptured paths.

    """

    host: str
    method: str
    path: str
    scope: str
    beyond_capture: bool | Unset = UNSET
    captured: list[RoutePolicyAffectedOperation] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        host = self.host

        method = self.method

        path = self.path

        scope = self.scope

        beyond_capture = self.beyond_capture

        captured: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.captured, Unset):
            captured = []
            for captured_item_data in self.captured:
                captured_item = captured_item_data.to_dict()
                captured.append(captured_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "host": host,
                "method": method,
                "path": path,
                "scope": scope,
            }
        )
        if beyond_capture is not UNSET:
            field_dict["beyond_capture"] = beyond_capture
        if captured is not UNSET:
            field_dict["captured"] = captured

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_policy_affected_operation import RoutePolicyAffectedOperation

        d = dict(src_dict)
        host = d.pop("host")

        method = d.pop("method")

        path = d.pop("path")

        scope = d.pop("scope")

        beyond_capture = d.pop("beyond_capture", UNSET)

        _captured = d.pop("captured", UNSET)
        captured: list[RoutePolicyAffectedOperation] | Unset = UNSET
        if _captured is not UNSET:
            captured = []
            for captured_item_data in _captured:
                captured_item = RoutePolicyAffectedOperation.from_dict(captured_item_data)

                captured.append(captured_item)

        route_policy_impact = cls(
            host=host,
            method=method,
            path=path,
            scope=scope,
            beyond_capture=beyond_capture,
            captured=captured,
        )

        route_policy_impact.additional_properties = d
        return route_policy_impact

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
