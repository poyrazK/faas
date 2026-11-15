from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_prompt_message import OutboundAIPromptMessage
    from ..models.outbound_ai_prompt_params import OutboundAIPromptParams
    from ..models.outbound_ai_prompt_target import OutboundAIPromptTarget
    from ..models.outbound_ai_prompt_version_defaults import OutboundAIPromptVersionDefaults


T = TypeVar("T", bound="OutboundAIPromptVersion")


@_attrs_define
class OutboundAIPromptVersion:
    """One immutable version of a managed AI prompt."""

    version: int
    messages: list[OutboundAIPromptMessage]
    variables: list[str]
    """The distinct placeholder names"""
    defaults: OutboundAIPromptVersionDefaults
    target: OutboundAIPromptTarget
    """Where a version's calls go: an openai-format route, or a managed AI integration with a model. Exactly one of
    route_id and integration_id is set; on a route target model is optional and is the request's model."""
    params: OutboundAIPromptParams
    """The sampling settings a version sends."""
    note: str
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        messages = []
        for messages_item_data in self.messages:
            messages_item = messages_item_data.to_dict()
            messages.append(messages_item)

        variables = self.variables

        defaults = self.defaults.to_dict()

        target = self.target.to_dict()

        params = self.params.to_dict()

        note = self.note

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "messages": messages,
                "variables": variables,
                "defaults": defaults,
                "target": target,
                "params": params,
                "note": note,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_prompt_message import OutboundAIPromptMessage
        from ..models.outbound_ai_prompt_params import OutboundAIPromptParams
        from ..models.outbound_ai_prompt_target import OutboundAIPromptTarget
        from ..models.outbound_ai_prompt_version_defaults import OutboundAIPromptVersionDefaults

        d = dict(src_dict)
        version = d.pop("version")

        messages = []
        _messages = d.pop("messages")
        for messages_item_data in _messages:
            messages_item = OutboundAIPromptMessage.from_dict(messages_item_data)

            messages.append(messages_item)

        variables = cast(list[str], d.pop("variables"))

        defaults = OutboundAIPromptVersionDefaults.from_dict(d.pop("defaults"))

        target = OutboundAIPromptTarget.from_dict(d.pop("target"))

        params = OutboundAIPromptParams.from_dict(d.pop("params"))

        note = d.pop("note")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        outbound_ai_prompt_version = cls(
            version=version,
            messages=messages,
            variables=variables,
            defaults=defaults,
            target=target,
            params=params,
            note=note,
            created_at=created_at,
        )

        outbound_ai_prompt_version.additional_properties = d
        return outbound_ai_prompt_version

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
