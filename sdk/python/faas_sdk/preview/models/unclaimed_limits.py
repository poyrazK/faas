from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="UnclaimedLimits")


@_attrs_define
class UnclaimedLimits:
    """What an account deployed without an owner may use until it is claimed."""

    plan: str
    max_apps: int
    max_ram_mb: int
    blocked: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan = self.plan

        max_apps = self.max_apps

        max_ram_mb = self.max_ram_mb

        blocked = self.blocked

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
                "max_apps": max_apps,
                "max_ram_mb": max_ram_mb,
                "blocked": blocked,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan = d.pop("plan")

        max_apps = d.pop("max_apps")

        max_ram_mb = d.pop("max_ram_mb")

        blocked = cast(list[str], d.pop("blocked"))

        unclaimed_limits = cls(
            plan=plan,
            max_apps=max_apps,
            max_ram_mb=max_ram_mb,
            blocked=blocked,
        )

        unclaimed_limits.additional_properties = d
        return unclaimed_limits

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
