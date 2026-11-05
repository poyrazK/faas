from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.obs_recording_alert_reference import ObsRecordingAlertReference


T = TypeVar("T", bound="ObsRecordingAlertReferences")


@_attrs_define
class ObsRecordingAlertReferences:
    """Complete count of this known catalog reference list before its five-item preview; unresolved dependencies can remain
    outside a dependent-alert list.

    """

    items: list[ObsRecordingAlertReference]
    total_items: int
    limit: Literal[5]
    truncated: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        total_items = self.total_items

        limit = self.limit

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "total_items": total_items,
                "limit": limit,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_recording_alert_reference import ObsRecordingAlertReference

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRecordingAlertReference.from_dict(items_item_data)

            items.append(items_item)

        total_items = d.pop("total_items")

        limit = cast(Literal[5], d.pop("limit"))
        if limit != 5:
            raise ValueError(f"limit must match const 5, got '{limit}'")

        truncated = d.pop("truncated")

        obs_recording_alert_references = cls(
            items=items,
            total_items=total_items,
            limit=limit,
            truncated=truncated,
        )

        obs_recording_alert_references.additional_properties = d
        return obs_recording_alert_references

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
