from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_inbox_message_response import ManagedRealtimeInboxMessageResponse


T = TypeVar("T", bound="ManagedRealtimeInboxResponse")


@_attrs_define
class ManagedRealtimeInboxResponse:
    """Principal inbox page with retention bounds and consumer checkpoint."""

    messages: list[ManagedRealtimeInboxMessageResponse]
    oldest_sequence: int
    latest_sequence: int
    history_unavailable: bool
    has_more: bool
    consumer: str | Unset = UNSET
    acknowledged_sequence: int | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        messages = []
        for messages_item_data in self.messages:
            messages_item = messages_item_data.to_dict()
            messages.append(messages_item)

        oldest_sequence = self.oldest_sequence

        latest_sequence = self.latest_sequence

        history_unavailable = self.history_unavailable

        has_more = self.has_more

        consumer = self.consumer

        acknowledged_sequence: int | None | Unset
        if isinstance(self.acknowledged_sequence, Unset):
            acknowledged_sequence = UNSET
        else:
            acknowledged_sequence = self.acknowledged_sequence

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "messages": messages,
                "oldest_sequence": oldest_sequence,
                "latest_sequence": latest_sequence,
                "history_unavailable": history_unavailable,
                "has_more": has_more,
            }
        )
        if consumer is not UNSET:
            field_dict["consumer"] = consumer
        if acknowledged_sequence is not UNSET:
            field_dict["acknowledged_sequence"] = acknowledged_sequence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_inbox_message_response import ManagedRealtimeInboxMessageResponse

        d = dict(src_dict)
        messages = []
        _messages = d.pop("messages")
        for messages_item_data in _messages:
            messages_item = ManagedRealtimeInboxMessageResponse.from_dict(messages_item_data)

            messages.append(messages_item)

        oldest_sequence = d.pop("oldest_sequence")

        latest_sequence = d.pop("latest_sequence")

        history_unavailable = d.pop("history_unavailable")

        has_more = d.pop("has_more")

        consumer = d.pop("consumer", UNSET)

        def _parse_acknowledged_sequence(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        acknowledged_sequence = _parse_acknowledged_sequence(d.pop("acknowledged_sequence", UNSET))

        managed_realtime_inbox_response = cls(
            messages=messages,
            oldest_sequence=oldest_sequence,
            latest_sequence=latest_sequence,
            history_unavailable=history_unavailable,
            has_more=has_more,
            consumer=consumer,
            acknowledged_sequence=acknowledged_sequence,
        )

        managed_realtime_inbox_response.additional_properties = d
        return managed_realtime_inbox_response

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
