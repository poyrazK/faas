from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.route_split import RouteSplit


T = TypeVar("T", bound="RouteSplitsResponse")


@_attrs_define
class RouteSplitsResponse:
    splits: list[RouteSplit]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        splits = []
        for splits_item_data in self.splits:
            splits_item = splits_item_data.to_dict()
            splits.append(splits_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "splits": splits,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_split import RouteSplit

        d = dict(src_dict)
        splits = []
        _splits = d.pop("splits")
        for splits_item_data in _splits:
            splits_item = RouteSplit.from_dict(splits_item_data)

            splits.append(splits_item)

        route_splits_response = cls(
            splits=splits,
        )

        route_splits_response.additional_properties = d
        return route_splits_response

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
