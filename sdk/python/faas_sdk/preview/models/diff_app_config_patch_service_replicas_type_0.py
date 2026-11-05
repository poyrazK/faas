from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DiffAppConfigPatchServiceReplicasType0")


@_attrs_define
class DiffAppConfigPatchServiceReplicasType0:
    """Replica policy for service mode."""

    min_: int | Unset = UNSET
    max_: int | Unset = UNSET
    desired: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        min_ = self.min_

        max_ = self.max_

        desired = self.desired

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if min_ is not UNSET:
            field_dict["min"] = min_
        if max_ is not UNSET:
            field_dict["max"] = max_
        if desired is not UNSET:
            field_dict["desired"] = desired

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        min_ = d.pop("min", UNSET)

        max_ = d.pop("max", UNSET)

        desired = d.pop("desired", UNSET)

        diff_app_config_patch_service_replicas_type_0 = cls(
            min_=min_,
            max_=max_,
            desired=desired,
        )

        diff_app_config_patch_service_replicas_type_0.additional_properties = d
        return diff_app_config_patch_service_replicas_type_0

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
