from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.operator_platform_flag import OperatorPlatformFlag
    from ..models.operator_platform_flag_groups import OperatorPlatformFlagGroups


T = TypeVar("T", bound="OperatorPlatformFlagList")


@_attrs_define
class OperatorPlatformFlagList:
    """Every platform flag and the shared account groups."""

    items: list[OperatorPlatformFlag]
    groups: OperatorPlatformFlagGroups
    """Shared named account sets referenced by platform-flag rules."""
    generated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        groups = self.groups.to_dict()

        generated_at = self.generated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "groups": groups,
                "generated_at": generated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operator_platform_flag import OperatorPlatformFlag
        from ..models.operator_platform_flag_groups import OperatorPlatformFlagGroups

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperatorPlatformFlag.from_dict(items_item_data)

            items.append(items_item)

        groups = OperatorPlatformFlagGroups.from_dict(d.pop("groups"))

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        operator_platform_flag_list = cls(
            items=items,
            groups=groups,
            generated_at=generated_at,
        )

        operator_platform_flag_list.additional_properties = d
        return operator_platform_flag_list

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
