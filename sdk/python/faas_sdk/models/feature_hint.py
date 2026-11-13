from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="FeatureHint")


@_attrs_define
class FeatureHint:
    """One Gregale behavior or feature that applies to the scanned source: what the plan will do, how to change it, and the
    exact CLI commands. IDs are stable.

    """

    id: str
    """Stable hint ID, such as health-default-path or public-needs-api-key"""
    applied: str
    """What Gregale will do with this source"""
    workloads: list[str] | Unset = UNSET
    """Workloads the hint is about"""
    change: str | Unset = UNSET
    """How to change it"""
    commands: list[str] | Unset = UNSET
    """Exact gregale commands; <placeholders> need a value"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        applied = self.applied

        workloads: list[str] | Unset = UNSET
        if not isinstance(self.workloads, Unset):
            workloads = self.workloads

        change = self.change

        commands: list[str] | Unset = UNSET
        if not isinstance(self.commands, Unset):
            commands = self.commands

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "applied": applied,
            }
        )
        if workloads is not UNSET:
            field_dict["workloads"] = workloads
        if change is not UNSET:
            field_dict["change"] = change
        if commands is not UNSET:
            field_dict["commands"] = commands

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        applied = d.pop("applied")

        workloads = cast(list[str], d.pop("workloads", UNSET))

        change = d.pop("change", UNSET)

        commands = cast(list[str], d.pop("commands", UNSET))

        feature_hint = cls(
            id=id,
            applied=applied,
            workloads=workloads,
            change=change,
            commands=commands,
        )

        feature_hint.additional_properties = d
        return feature_hint

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
