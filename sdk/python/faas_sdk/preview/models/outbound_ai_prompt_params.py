from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_prompt_params_response_format import OutboundAIPromptParamsResponseFormat


T = TypeVar("T", bound="OutboundAIPromptParams")


@_attrs_define
class OutboundAIPromptParams:
    """The sampling settings a version sends."""

    temperature: float | Unset = UNSET
    top_p: float | Unset = UNSET
    max_tokens: int | Unset = UNSET
    stop: list[str] | Unset = UNSET
    response_format: OutboundAIPromptParamsResponseFormat | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        temperature = self.temperature

        top_p = self.top_p

        max_tokens = self.max_tokens

        stop: list[str] | Unset = UNSET
        if not isinstance(self.stop, Unset):
            stop = self.stop

        response_format: dict[str, Any] | Unset = UNSET
        if not isinstance(self.response_format, Unset):
            response_format = self.response_format.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if temperature is not UNSET:
            field_dict["temperature"] = temperature
        if top_p is not UNSET:
            field_dict["top_p"] = top_p
        if max_tokens is not UNSET:
            field_dict["max_tokens"] = max_tokens
        if stop is not UNSET:
            field_dict["stop"] = stop
        if response_format is not UNSET:
            field_dict["response_format"] = response_format

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_prompt_params_response_format import OutboundAIPromptParamsResponseFormat

        d = dict(src_dict)
        temperature = d.pop("temperature", UNSET)

        top_p = d.pop("top_p", UNSET)

        max_tokens = d.pop("max_tokens", UNSET)

        stop = cast(list[str], d.pop("stop", UNSET))

        _response_format = d.pop("response_format", UNSET)
        response_format: OutboundAIPromptParamsResponseFormat | Unset
        if isinstance(_response_format, Unset):
            response_format = UNSET
        else:
            response_format = OutboundAIPromptParamsResponseFormat.from_dict(_response_format)

        outbound_ai_prompt_params = cls(
            temperature=temperature,
            top_p=top_p,
            max_tokens=max_tokens,
            stop=stop,
            response_format=response_format,
        )

        outbound_ai_prompt_params.additional_properties = d
        return outbound_ai_prompt_params

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
