from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.queue_dead_letter_message import QueueDeadLetterMessage


T = TypeVar("T", bound="QueueDeadLetterResponse")


@_attrs_define
class QueueDeadLetterResponse:
    """200 — a page of dead-letter rows ordered by created_at DESC, id DESC."""

    app_slug: str
    messages: list[QueueDeadLetterMessage]
    next_before: str | Unset = UNSET
    """Cursor for the previous page; absent on the final page."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        messages = []
        for messages_item_data in self.messages:
            messages_item = messages_item_data.to_dict()
            messages.append(messages_item)

        next_before = self.next_before

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_slug": app_slug,
                "messages": messages,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.queue_dead_letter_message import QueueDeadLetterMessage

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        messages = []
        _messages = d.pop("messages")
        for messages_item_data in _messages:
            messages_item = QueueDeadLetterMessage.from_dict(messages_item_data)

            messages.append(messages_item)

        next_before = d.pop("next_before", UNSET)

        queue_dead_letter_response = cls(
            app_slug=app_slug,
            messages=messages,
            next_before=next_before,
        )

        queue_dead_letter_response.additional_properties = d
        return queue_dead_letter_response

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
