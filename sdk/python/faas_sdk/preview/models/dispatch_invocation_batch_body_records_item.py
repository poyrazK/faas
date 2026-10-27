from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.dispatch_invocation_batch_body_records_item_headers import (
        DispatchInvocationBatchBodyRecordsItemHeaders,
    )
    from ..models.dispatch_invocation_batch_body_records_item_metadata import (
        DispatchInvocationBatchBodyRecordsItemMetadata,
    )


T = TypeVar("T", bound="DispatchInvocationBatchBodyRecordsItem")


@_attrs_define
class DispatchInvocationBatchBodyRecordsItem:
    item_identifier: str
    payload_b64: str
    invocation_id: UUID | Unset = UNSET
    """Internal durable queue invocation ID; must equal item_identifier and have a current claimed lease."""
    invocation_attempt: int | Unset = UNSET
    """Required with invocation_id; fences delivery to the current durable queue claim attempt."""
    headers: DispatchInvocationBatchBodyRecordsItemHeaders | Unset = UNSET
    metadata: DispatchInvocationBatchBodyRecordsItemMetadata | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        item_identifier = self.item_identifier

        payload_b64 = self.payload_b64

        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        invocation_attempt = self.invocation_attempt

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "item_identifier": item_identifier,
                "payload_b64": payload_b64,
            }
        )
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if invocation_attempt is not UNSET:
            field_dict["invocation_attempt"] = invocation_attempt
        if headers is not UNSET:
            field_dict["headers"] = headers
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dispatch_invocation_batch_body_records_item_headers import (
            DispatchInvocationBatchBodyRecordsItemHeaders,
        )
        from ..models.dispatch_invocation_batch_body_records_item_metadata import (
            DispatchInvocationBatchBodyRecordsItemMetadata,
        )

        d = dict(src_dict)
        item_identifier = d.pop("item_identifier")

        payload_b64 = d.pop("payload_b64")

        _invocation_id = d.pop("invocation_id", UNSET)
        invocation_id: UUID | Unset
        if isinstance(_invocation_id, Unset):
            invocation_id = UNSET
        else:
            invocation_id = UUID(_invocation_id)

        invocation_attempt = d.pop("invocation_attempt", UNSET)

        _headers = d.pop("headers", UNSET)
        headers: DispatchInvocationBatchBodyRecordsItemHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = DispatchInvocationBatchBodyRecordsItemHeaders.from_dict(_headers)

        _metadata = d.pop("metadata", UNSET)
        metadata: DispatchInvocationBatchBodyRecordsItemMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = DispatchInvocationBatchBodyRecordsItemMetadata.from_dict(_metadata)

        dispatch_invocation_batch_body_records_item = cls(
            item_identifier=item_identifier,
            payload_b64=payload_b64,
            invocation_id=invocation_id,
            invocation_attempt=invocation_attempt,
            headers=headers,
            metadata=metadata,
        )

        dispatch_invocation_batch_body_records_item.additional_properties = d
        return dispatch_invocation_batch_body_records_item

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
