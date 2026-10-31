from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.route_removal_mapping_method import RouteRemovalMappingMethod, check_route_removal_mapping_method
from ..models.route_removal_mapping_successor_method import (
    RouteRemovalMappingSuccessorMethod,
    check_route_removal_mapping_successor_method,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteRemovalMapping")


@_attrs_define
class RouteRemovalMapping:
    """Removed operation and its optional successor operation."""

    method: RouteRemovalMappingMethod
    path: str
    successor_method: RouteRemovalMappingSuccessorMethod | Unset = UNSET
    successor_path: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        successor_method: str | Unset = UNSET
        if not isinstance(self.successor_method, Unset):
            successor_method = self.successor_method

        successor_path = self.successor_path

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "method": method,
                "path": path,
            }
        )
        if successor_method is not UNSET:
            field_dict["successor_method"] = successor_method
        if successor_path is not UNSET:
            field_dict["successor_path"] = successor_path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = check_route_removal_mapping_method(d.pop("method"))

        path = d.pop("path")

        _successor_method = d.pop("successor_method", UNSET)
        successor_method: RouteRemovalMappingSuccessorMethod | Unset
        if isinstance(_successor_method, Unset):
            successor_method = UNSET
        else:
            successor_method = check_route_removal_mapping_successor_method(_successor_method)

        successor_path = d.pop("successor_path", UNSET)

        route_removal_mapping = cls(
            method=method,
            path=path,
            successor_method=successor_method,
            successor_path=successor_path,
        )

        return route_removal_mapping
