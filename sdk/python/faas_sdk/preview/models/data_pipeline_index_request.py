from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.data_pipeline_index_request_action import (
    DataPipelineIndexRequestAction,
    check_data_pipeline_index_request_action,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_index_order import DataPipelineIndexOrder


T = TypeVar("T", bound="DataPipelineIndexRequest")


@_attrs_define
class DataPipelineIndexRequest:
    """Structured mirror query shape with no SQL or filter values. preview queues metadata inspection; approve accepts only
    a frozen advice_id; discard clears unapproved advice. At most four keys including the tenant policy expression.

    """

    expected_revision: int
    action: DataPipelineIndexRequestAction
    advice_id: str | Unset = UNSET
    table: str | Unset = UNSET
    filters: list[str] | Unset = UNSET
    order_by: list[DataPipelineIndexOrder] | Unset = UNSET
    max_index_bytes: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        action: str = self.action

        advice_id = self.advice_id

        table = self.table

        filters: list[str] | Unset = UNSET
        if not isinstance(self.filters, Unset):
            filters = self.filters

        order_by: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.order_by, Unset):
            order_by = []
            for order_by_item_data in self.order_by:
                order_by_item = order_by_item_data.to_dict()
                order_by.append(order_by_item)

        max_index_bytes = self.max_index_bytes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "action": action,
            }
        )
        if advice_id is not UNSET:
            field_dict["advice_id"] = advice_id
        if table is not UNSET:
            field_dict["table"] = table
        if filters is not UNSET:
            field_dict["filters"] = filters
        if order_by is not UNSET:
            field_dict["order_by"] = order_by
        if max_index_bytes is not UNSET:
            field_dict["max_index_bytes"] = max_index_bytes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_index_order import DataPipelineIndexOrder

        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        action = check_data_pipeline_index_request_action(d.pop("action"))

        advice_id = d.pop("advice_id", UNSET)

        table = d.pop("table", UNSET)

        filters = cast(list[str], d.pop("filters", UNSET))

        _order_by = d.pop("order_by", UNSET)
        order_by: list[DataPipelineIndexOrder] | Unset = UNSET
        if _order_by is not UNSET:
            order_by = []
            for order_by_item_data in _order_by:
                order_by_item = DataPipelineIndexOrder.from_dict(order_by_item_data)

                order_by.append(order_by_item)

        max_index_bytes = d.pop("max_index_bytes", UNSET)

        data_pipeline_index_request = cls(
            expected_revision=expected_revision,
            action=action,
            advice_id=advice_id,
            table=table,
            filters=filters,
            order_by=order_by,
            max_index_bytes=max_index_bytes,
        )

        return data_pipeline_index_request
