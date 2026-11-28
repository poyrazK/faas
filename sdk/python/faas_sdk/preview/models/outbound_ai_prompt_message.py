from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_prompt_message_role import OutboundAIPromptMessageRole, check_outbound_ai_prompt_message_role

T = TypeVar("T", bound="OutboundAIPromptMessage")


@_attrs_define
class OutboundAIPromptMessage:
    """A templated message; content may hold {{name}} placeholders."""

    role: OutboundAIPromptMessageRole
    content: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        role: str = self.role

        content = self.content

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "role": role,
                "content": content,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        role = check_outbound_ai_prompt_message_role(d.pop("role"))

        content = d.pop("content")

        outbound_ai_prompt_message = cls(
            role=role,
            content=content,
        )

        outbound_ai_prompt_message.additional_properties = d
        return outbound_ai_prompt_message

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
