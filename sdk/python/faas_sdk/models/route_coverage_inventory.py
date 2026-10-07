from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteCoverageInventory")


@_attrs_define
class RouteCoverageInventory:
    """Provenance and completeness of the selected captured contract; raw contract content is excluded."""

    status: str
    source: str
    route_count: int
    code: str | Unset = UNSET
    deployment: str | Unset = UNSET
    sha256: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status = self.status

        source = self.source

        route_count = self.route_count

        code = self.code

        deployment = self.deployment

        sha256 = self.sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "source": source,
                "route_count": route_count,
            }
        )
        if code is not UNSET:
            field_dict["code"] = code
        if deployment is not UNSET:
            field_dict["deployment"] = deployment
        if sha256 is not UNSET:
            field_dict["sha256"] = sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = d.pop("status")

        source = d.pop("source")

        route_count = d.pop("route_count")

        code = d.pop("code", UNSET)

        deployment = d.pop("deployment", UNSET)

        sha256 = d.pop("sha256", UNSET)

        route_coverage_inventory = cls(
            status=status,
            source=source,
            route_count=route_count,
            code=code,
            deployment=deployment,
            sha256=sha256,
        )

        route_coverage_inventory.additional_properties = d
        return route_coverage_inventory

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
