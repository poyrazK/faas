from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="Preview")


@_attrs_define
class Preview:
    """One self-serve platform preview (ADR 20261021080640)."""

    key: str
    label: str
    description: str
    joined: bool
    """Whether you (or, under /v1/orgs/{slug}, the organization) opted in."""
    eligible: bool
    """Whether the preview is open to self-serve and your plan may join it."""
    enabled: bool
    """Whether the preview is on for you now, for any reason."""
    retire_by: datetime.date | Unset = UNSET
    """Planned end of the preview, by general availability or removal."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        label = self.label

        description = self.description

        joined = self.joined

        eligible = self.eligible

        enabled = self.enabled

        retire_by: str | Unset = UNSET
        if not isinstance(self.retire_by, Unset):
            retire_by = self.retire_by.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "label": label,
                "description": description,
                "joined": joined,
                "eligible": eligible,
                "enabled": enabled,
            }
        )
        if retire_by is not UNSET:
            field_dict["retire_by"] = retire_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        label = d.pop("label")

        description = d.pop("description")

        joined = d.pop("joined")

        eligible = d.pop("eligible")

        enabled = d.pop("enabled")

        _retire_by = d.pop("retire_by", UNSET)
        retire_by: datetime.date | Unset
        if isinstance(_retire_by, Unset):
            retire_by = UNSET
        else:
            retire_by = datetime.date.fromisoformat(_retire_by)

        preview = cls(
            key=key,
            label=label,
            description=description,
            joined=joined,
            eligible=eligible,
            enabled=enabled,
            retire_by=retire_by,
        )

        preview.additional_properties = d
        return preview

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
