from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIUsageItem")


@_attrs_define
class OutboundAIUsageItem:
    """One UTC-day rollup for a caller and model. input_tokens includes cached reads and cache writes, which are also
    reported separately.

    """

    caller: str
    """App UUID, or "runs" for stateless Runs."""
    model: str
    """Provider-reported model; "other" collects models beyond the daily distinct-model cap and "unknown" covers
    responses without one."""
    requests: int
    error_requests: int
    usage_unknown_requests: int
    """Successful calls whose response reported no usage, such as an OpenAI stream without
    stream_options.include_usage."""
    input_tokens: int
    cached_input_tokens: int
    cache_write_tokens: int
    output_tokens: int
    cost_millicents: int | Unset = UNSET
    """Priced from the list in effect at each call, else the built-in catalog, and rounded up per call; zero for an
    unpriced model."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        caller = self.caller

        model = self.model

        requests = self.requests

        error_requests = self.error_requests

        usage_unknown_requests = self.usage_unknown_requests

        input_tokens = self.input_tokens

        cached_input_tokens = self.cached_input_tokens

        cache_write_tokens = self.cache_write_tokens

        output_tokens = self.output_tokens

        cost_millicents = self.cost_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "caller": caller,
                "model": model,
                "requests": requests,
                "error_requests": error_requests,
                "usage_unknown_requests": usage_unknown_requests,
                "input_tokens": input_tokens,
                "cached_input_tokens": cached_input_tokens,
                "cache_write_tokens": cache_write_tokens,
                "output_tokens": output_tokens,
            }
        )
        if cost_millicents is not UNSET:
            field_dict["cost_millicents"] = cost_millicents

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        caller = d.pop("caller")

        model = d.pop("model")

        requests = d.pop("requests")

        error_requests = d.pop("error_requests")

        usage_unknown_requests = d.pop("usage_unknown_requests")

        input_tokens = d.pop("input_tokens")

        cached_input_tokens = d.pop("cached_input_tokens")

        cache_write_tokens = d.pop("cache_write_tokens")

        output_tokens = d.pop("output_tokens")

        cost_millicents = d.pop("cost_millicents", UNSET)

        outbound_ai_usage_item = cls(
            caller=caller,
            model=model,
            requests=requests,
            error_requests=error_requests,
            usage_unknown_requests=usage_unknown_requests,
            input_tokens=input_tokens,
            cached_input_tokens=cached_input_tokens,
            cache_write_tokens=cache_write_tokens,
            output_tokens=output_tokens,
            cost_millicents=cost_millicents,
        )

        outbound_ai_usage_item.additional_properties = d
        return outbound_ai_usage_item

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
