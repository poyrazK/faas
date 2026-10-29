from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_flag_self_serve_plans_item import (
    PlatformFlagSelfServePlansItem,
    check_platform_flag_self_serve_plans_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformFlagSelfServe")


@_attrs_define
class PlatformFlagSelfServe:
    """Lets customers join the preview themselves. An opted-in account or organization on an eligible plan gets the flag
    when no rule matched. Disabling it ignores existing opt-ins without deleting them.

    """

    enabled: bool
    plans: list[PlatformFlagSelfServePlansItem] | Unset = UNSET
    """Eligible plans; empty means every plan. A flag that releases a paid capability must list only entitled
    plans."""
    max_subjects: int | Unset = UNSET
    """Cap on opted-in accounts plus organizations; 0 means 5000."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        plans: list[str] | Unset = UNSET
        if not isinstance(self.plans, Unset):
            plans = []
            for plans_item_data in self.plans:
                plans_item: str = plans_item_data
                plans.append(plans_item)

        max_subjects = self.max_subjects

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
            }
        )
        if plans is not UNSET:
            field_dict["plans"] = plans
        if max_subjects is not UNSET:
            field_dict["max_subjects"] = max_subjects

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        _plans = d.pop("plans", UNSET)
        plans: list[PlatformFlagSelfServePlansItem] | Unset = UNSET
        if _plans is not UNSET:
            plans = []
            for plans_item_data in _plans:
                plans_item = check_platform_flag_self_serve_plans_item(plans_item_data)

                plans.append(plans_item)

        max_subjects = d.pop("max_subjects", UNSET)

        platform_flag_self_serve = cls(
            enabled=enabled,
            plans=plans,
            max_subjects=max_subjects,
        )

        platform_flag_self_serve.additional_properties = d
        return platform_flag_self_serve

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
