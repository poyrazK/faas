from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeHandoffCollectionValues")


@_attrs_define
class ObsRuntimeHandoffCollectionValues:
    """The reported collection-success bit and start of the last successful snapshot read, frozen on failure. A zero
    success report can also precede the first successful read.

    """

    success: bool
    last_success_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        success = self.success

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "success": success,
            }
        )
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        success = d.pop("success")

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        obs_runtime_handoff_collection_values = cls(
            success=success,
            last_success_at=last_success_at,
        )

        obs_runtime_handoff_collection_values.additional_properties = d
        return obs_runtime_handoff_collection_values

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
