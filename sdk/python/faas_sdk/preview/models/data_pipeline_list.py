from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.data_pipeline import DataPipeline


T = TypeVar("T", bound="DataPipelineList")


@_attrs_define
class DataPipelineList:
    """Bounded history of account-scoped managed data pipeline records."""

    items: list[DataPipeline]

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline import DataPipeline

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = DataPipeline.from_dict(items_item_data)

            items.append(items_item)

        data_pipeline_list = cls(
            items=items,
        )

        return data_pipeline_list
