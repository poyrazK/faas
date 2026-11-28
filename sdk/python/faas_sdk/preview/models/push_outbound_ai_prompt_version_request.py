from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_prompt_message import OutboundAIPromptMessage
    from ..models.outbound_ai_prompt_params import OutboundAIPromptParams
    from ..models.outbound_ai_prompt_target import OutboundAIPromptTarget
    from ..models.push_outbound_ai_prompt_version_request_defaults import PushOutboundAIPromptVersionRequestDefaults


T = TypeVar("T", bound="PushOutboundAIPromptVersionRequest")


@_attrs_define
class PushOutboundAIPromptVersionRequest:
    """A new version. messages, defaults and params together are at most 64 KiB."""

    messages: list[OutboundAIPromptMessage]
    target: OutboundAIPromptTarget
    """Where a version's calls go: an openai-format route, or a managed AI integration with a model. Exactly one of
    route_id and integration_id is set; on a route target model is optional and is the request's model."""
    description: str | Unset = UNSET
    """Replaces the prompt's description when set."""
    defaults: PushOutboundAIPromptVersionRequestDefaults | Unset = UNSET
    """Values for variables a call may omit."""
    params: OutboundAIPromptParams | Unset = UNSET
    """The sampling settings a version sends."""
    note: str | Unset = UNSET
    live: bool | Unset = UNSET
    """Make the version live. A prompt's first version always is."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        messages = []
        for messages_item_data in self.messages:
            messages_item = messages_item_data.to_dict()
            messages.append(messages_item)

        target = self.target.to_dict()

        description = self.description

        defaults: dict[str, Any] | Unset = UNSET
        if not isinstance(self.defaults, Unset):
            defaults = self.defaults.to_dict()

        params: dict[str, Any] | Unset = UNSET
        if not isinstance(self.params, Unset):
            params = self.params.to_dict()

        note = self.note

        live = self.live

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "messages": messages,
                "target": target,
            }
        )
        if description is not UNSET:
            field_dict["description"] = description
        if defaults is not UNSET:
            field_dict["defaults"] = defaults
        if params is not UNSET:
            field_dict["params"] = params
        if note is not UNSET:
            field_dict["note"] = note
        if live is not UNSET:
            field_dict["live"] = live

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_prompt_message import OutboundAIPromptMessage
        from ..models.outbound_ai_prompt_params import OutboundAIPromptParams
        from ..models.outbound_ai_prompt_target import OutboundAIPromptTarget
        from ..models.push_outbound_ai_prompt_version_request_defaults import PushOutboundAIPromptVersionRequestDefaults

        d = dict(src_dict)
        messages = []
        _messages = d.pop("messages")
        for messages_item_data in _messages:
            messages_item = OutboundAIPromptMessage.from_dict(messages_item_data)

            messages.append(messages_item)

        target = OutboundAIPromptTarget.from_dict(d.pop("target"))

        description = d.pop("description", UNSET)

        _defaults = d.pop("defaults", UNSET)
        defaults: PushOutboundAIPromptVersionRequestDefaults | Unset
        if isinstance(_defaults, Unset):
            defaults = UNSET
        else:
            defaults = PushOutboundAIPromptVersionRequestDefaults.from_dict(_defaults)

        _params = d.pop("params", UNSET)
        params: OutboundAIPromptParams | Unset
        if isinstance(_params, Unset):
            params = UNSET
        else:
            params = OutboundAIPromptParams.from_dict(_params)

        note = d.pop("note", UNSET)

        live = d.pop("live", UNSET)

        push_outbound_ai_prompt_version_request = cls(
            messages=messages,
            target=target,
            description=description,
            defaults=defaults,
            params=params,
            note=note,
            live=live,
        )

        push_outbound_ai_prompt_version_request.additional_properties = d
        return push_outbound_ai_prompt_version_request

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
