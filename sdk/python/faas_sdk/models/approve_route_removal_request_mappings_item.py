from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_removal_mapping_method import RouteRemovalMappingMethod, check_route_removal_mapping_method
from ..models.route_removal_mapping_successor_method import (
    RouteRemovalMappingSuccessorMethod,
    check_route_removal_mapping_successor_method,
)

T = TypeVar("T", bound="ApproveRouteRemovalRequestMappingsItem")


@_attrs_define
class ApproveRouteRemovalRequestMappingsItem:
    method: RouteRemovalMappingMethod
    path: str
    successor_method: RouteRemovalMappingSuccessorMethod
    successor_path: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        successor_method: str = self.successor_method

        successor_path = self.successor_path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "successor_method": successor_method,
                "successor_path": successor_path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = check_route_removal_mapping_method(d.pop("method"))

        path = d.pop("path")

        successor_method = check_route_removal_mapping_successor_method(d.pop("successor_method"))

        successor_path = d.pop("successor_path")

        approve_route_removal_request_mappings_item = cls(
            method=method,
            path=path,
            successor_method=successor_method,
            successor_path=successor_path,
        )

        approve_route_removal_request_mappings_item.additional_properties = d
        return approve_route_removal_request_mappings_item

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
