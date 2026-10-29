from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ReleaseDiffDatabase")


@_attrs_define
class ReleaseDiffDatabase:
    """How the releases' release commands, which run database migrations, differ."""

    changed: bool
    from_release_command: str | Unset = UNSET
    to_release_command: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        changed = self.changed

        from_release_command = self.from_release_command

        to_release_command = self.to_release_command

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "changed": changed,
            }
        )
        if from_release_command is not UNSET:
            field_dict["from_release_command"] = from_release_command
        if to_release_command is not UNSET:
            field_dict["to_release_command"] = to_release_command

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        changed = d.pop("changed")

        from_release_command = d.pop("from_release_command", UNSET)

        to_release_command = d.pop("to_release_command", UNSET)

        release_diff_database = cls(
            changed=changed,
            from_release_command=from_release_command,
            to_release_command=to_release_command,
        )

        release_diff_database.additional_properties = d
        return release_diff_database

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
