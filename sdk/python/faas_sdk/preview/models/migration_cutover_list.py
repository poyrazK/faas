from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.migration_cutover import MigrationCutover


T = TypeVar("T", bound="MigrationCutoverList")


@_attrs_define
class MigrationCutoverList:
    """Latest app-owned cutover plans, newest first. Up to twenty are retained; terminal plans may be pruned after ninety
    days.

    """

    plans: list[MigrationCutover]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plans = []
        for plans_item_data in self.plans:
            plans_item = plans_item_data.to_dict()
            plans.append(plans_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plans": plans,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.migration_cutover import MigrationCutover

        d = dict(src_dict)
        plans = []
        _plans = d.pop("plans")
        for plans_item_data in _plans:
            plans_item = MigrationCutover.from_dict(plans_item_data)

            plans.append(plans_item)

        migration_cutover_list = cls(
            plans=plans,
        )

        migration_cutover_list.additional_properties = d
        return migration_cutover_list

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
