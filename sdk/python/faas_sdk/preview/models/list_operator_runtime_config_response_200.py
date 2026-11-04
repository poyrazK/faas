from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.operator_runtime_config import OperatorRuntimeConfig


T = TypeVar("T", bound="ListOperatorRuntimeConfigResponse200")


@_attrs_define
class ListOperatorRuntimeConfigResponse200:
    items: list[OperatorRuntimeConfig]
    generated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        generated_at = self.generated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "generated_at": generated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operator_runtime_config import OperatorRuntimeConfig

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperatorRuntimeConfig.from_dict(items_item_data)

            items.append(items_item)

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        list_operator_runtime_config_response_200 = cls(
            items=items,
            generated_at=generated_at,
        )

        list_operator_runtime_config_response_200.additional_properties = d
        return list_operator_runtime_config_response_200

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
