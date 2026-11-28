from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.release_freeze import ReleaseFreeze


T = TypeVar("T", bound="ListReleaseFreezesResponse")


@_attrs_define
class ListReleaseFreezesResponse:
    """Unlifted freezes that have not ended, oldest first."""

    freezes: list[ReleaseFreeze]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        freezes = []
        for freezes_item_data in self.freezes:
            freezes_item = freezes_item_data.to_dict()
            freezes.append(freezes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "freezes": freezes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_freeze import ReleaseFreeze

        d = dict(src_dict)
        freezes = []
        _freezes = d.pop("freezes")
        for freezes_item_data in _freezes:
            freezes_item = ReleaseFreeze.from_dict(freezes_item_data)

            freezes.append(freezes_item)

        list_release_freezes_response = cls(
            freezes=freezes,
        )

        list_release_freezes_response.additional_properties = d
        return list_release_freezes_response

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
