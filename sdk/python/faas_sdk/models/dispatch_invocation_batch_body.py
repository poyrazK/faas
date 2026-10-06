from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.dispatch_invocation_batch_body_records_item import DispatchInvocationBatchBodyRecordsItem


T = TypeVar("T", bound="DispatchInvocationBatchBody")


@_attrs_define
class DispatchInvocationBatchBody:
    invocation_id: str
    """Synthetic batch identity, typically trigger- followed by the trigger UUID."""
    trigger_id: UUID
    app_id: UUID
    records: list[DispatchInvocationBatchBodyRecordsItem]
    source: str | Unset = UNSET
    """Internal dispatch source; esm for trigger and durable queue batches."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invocation_id = self.invocation_id

        trigger_id = str(self.trigger_id)

        app_id = str(self.app_id)

        records = []
        for records_item_data in self.records:
            records_item = records_item_data.to_dict()
            records.append(records_item)

        source = self.source

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invocation_id": invocation_id,
                "trigger_id": trigger_id,
                "app_id": app_id,
                "records": records,
            }
        )
        if source is not UNSET:
            field_dict["source"] = source

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dispatch_invocation_batch_body_records_item import DispatchInvocationBatchBodyRecordsItem

        d = dict(src_dict)
        invocation_id = d.pop("invocation_id")

        trigger_id = UUID(d.pop("trigger_id"))

        app_id = UUID(d.pop("app_id"))

        records = []
        _records = d.pop("records")
        for records_item_data in _records:
            records_item = DispatchInvocationBatchBodyRecordsItem.from_dict(records_item_data)

            records.append(records_item)

        source = d.pop("source", UNSET)

        dispatch_invocation_batch_body = cls(
            invocation_id=invocation_id,
            trigger_id=trigger_id,
            app_id=app_id,
            records=records,
            source=source,
        )

        dispatch_invocation_batch_body.additional_properties = d
        return dispatch_invocation_batch_body

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
