from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.tool_invocation_intent import ToolInvocationIntent


T = TypeVar("T", bound="PrivateToolInvocationRequest")


@_attrs_define
class PrivateToolInvocationRequest:
    """Private invocation intent plus bounded transient argument JSON. No credential value or secret reference is accepted."""

    grant_id: UUID
    intent: ToolInvocationIntent
    """Explicit one-call consent with a canonical argument digest and optional opaque credential binding; no
    argument bodies or credentials."""
    arguments_json: str
    """Canonical JSON argument object matching the intent digest"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grant_id = str(self.grant_id)

        intent = self.intent.to_dict()

        arguments_json = self.arguments_json

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grant_id": grant_id,
                "intent": intent,
                "arguments_json": arguments_json,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_invocation_intent import ToolInvocationIntent

        d = dict(src_dict)
        grant_id = UUID(d.pop("grant_id"))

        intent = ToolInvocationIntent.from_dict(d.pop("intent"))

        arguments_json = d.pop("arguments_json")

        private_tool_invocation_request = cls(
            grant_id=grant_id,
            intent=intent,
            arguments_json=arguments_json,
        )

        private_tool_invocation_request.additional_properties = d
        return private_tool_invocation_request

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
