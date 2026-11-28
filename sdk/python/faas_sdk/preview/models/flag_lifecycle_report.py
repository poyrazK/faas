from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.flag_lifecycle import FlagLifecycle


T = TypeVar("T", bound="FlagLifecycleReport")


@_attrs_define
class FlagLifecycleReport:
    """Lifecycle of every flag in the current configuration."""

    config_version: int
    generated_at: datetime.datetime
    flags: list[FlagLifecycle]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        config_version = self.config_version

        generated_at = self.generated_at.isoformat()

        flags = []
        for flags_item_data in self.flags:
            flags_item = flags_item_data.to_dict()
            flags.append(flags_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "config_version": config_version,
                "generated_at": generated_at,
                "flags": flags,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_lifecycle import FlagLifecycle

        d = dict(src_dict)
        config_version = d.pop("config_version")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        flags = []
        _flags = d.pop("flags")
        for flags_item_data in _flags:
            flags_item = FlagLifecycle.from_dict(flags_item_data)

            flags.append(flags_item)

        flag_lifecycle_report = cls(
            config_version=config_version,
            generated_at=generated_at,
            flags=flags,
        )

        flag_lifecycle_report.additional_properties = d
        return flag_lifecycle_report

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
