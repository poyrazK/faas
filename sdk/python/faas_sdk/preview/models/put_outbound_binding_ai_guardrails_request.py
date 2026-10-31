from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PutOutboundBindingAIGuardrailsRequest")


@_attrs_define
class PutOutboundBindingAIGuardrailsRequest:
    """Replace an app's AI guardrails on one integration. Null fields remove that guardrail."""

    requests_per_minute: int | None | Unset = UNSET
    tokens_per_minute: int | None | Unset = UNSET
    allowed_models: list[str] | None | Unset = UNSET
    max_output_tokens: int | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        requests_per_minute: int | None | Unset
        if isinstance(self.requests_per_minute, Unset):
            requests_per_minute = UNSET
        else:
            requests_per_minute = self.requests_per_minute

        tokens_per_minute: int | None | Unset
        if isinstance(self.tokens_per_minute, Unset):
            tokens_per_minute = UNSET
        else:
            tokens_per_minute = self.tokens_per_minute

        allowed_models: list[str] | None | Unset
        if isinstance(self.allowed_models, Unset):
            allowed_models = UNSET
        elif isinstance(self.allowed_models, list):
            allowed_models = self.allowed_models

        else:
            allowed_models = self.allowed_models

        max_output_tokens: int | None | Unset
        if isinstance(self.max_output_tokens, Unset):
            max_output_tokens = UNSET
        else:
            max_output_tokens = self.max_output_tokens

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if requests_per_minute is not UNSET:
            field_dict["requests_per_minute"] = requests_per_minute
        if tokens_per_minute is not UNSET:
            field_dict["tokens_per_minute"] = tokens_per_minute
        if allowed_models is not UNSET:
            field_dict["allowed_models"] = allowed_models
        if max_output_tokens is not UNSET:
            field_dict["max_output_tokens"] = max_output_tokens

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_requests_per_minute(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        requests_per_minute = _parse_requests_per_minute(d.pop("requests_per_minute", UNSET))

        def _parse_tokens_per_minute(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        tokens_per_minute = _parse_tokens_per_minute(d.pop("tokens_per_minute", UNSET))

        def _parse_allowed_models(data: object) -> list[str] | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                allowed_models_type_0 = cast(list[str], data)

                return allowed_models_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[str] | None | Unset, data)

        allowed_models = _parse_allowed_models(d.pop("allowed_models", UNSET))

        def _parse_max_output_tokens(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        max_output_tokens = _parse_max_output_tokens(d.pop("max_output_tokens", UNSET))

        put_outbound_binding_ai_guardrails_request = cls(
            requests_per_minute=requests_per_minute,
            tokens_per_minute=tokens_per_minute,
            allowed_models=allowed_models,
            max_output_tokens=max_output_tokens,
        )

        put_outbound_binding_ai_guardrails_request.additional_properties = d
        return put_outbound_binding_ai_guardrails_request

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
