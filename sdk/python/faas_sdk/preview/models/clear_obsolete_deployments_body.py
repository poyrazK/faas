from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ClearObsoleteDeploymentsBody")


@_attrs_define
class ClearObsoleteDeploymentsBody:
    older_than: str | Unset = UNSET
    """Go duration (e.g. 168h)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        older_than = self.older_than

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if older_than is not UNSET:
            field_dict["older_than"] = older_than

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        older_than = d.pop("older_than", UNSET)

        clear_obsolete_deployments_body = cls(
            older_than=older_than,
        )

        clear_obsolete_deployments_body.additional_properties = d
        return clear_obsolete_deployments_body

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
