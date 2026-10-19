from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundAITagUsageItem")


@_attrs_define
class OutboundAITagUsageItem:
    """One tag's UTC-day AI usage on an integration (ADR-1436); tokens is input plus output. A key's tags past the day's
    cap of distinct tags add to its overflow row, <key>=~other.

    """

    tag: str
    requests: int
    error_requests: int
    input_tokens: int
    output_tokens: int
    tokens: int
    cost_millicents: int
    daily_cost_limit_millicents: int | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tag = self.tag

        requests = self.requests

        error_requests = self.error_requests

        input_tokens = self.input_tokens

        output_tokens = self.output_tokens

        tokens = self.tokens

        cost_millicents = self.cost_millicents

        daily_cost_limit_millicents: int | None
        daily_cost_limit_millicents = self.daily_cost_limit_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tag": tag,
                "requests": requests,
                "error_requests": error_requests,
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "tokens": tokens,
                "cost_millicents": cost_millicents,
                "daily_cost_limit_millicents": daily_cost_limit_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        tag = d.pop("tag")

        requests = d.pop("requests")

        error_requests = d.pop("error_requests")

        input_tokens = d.pop("input_tokens")

        output_tokens = d.pop("output_tokens")

        tokens = d.pop("tokens")

        cost_millicents = d.pop("cost_millicents")

        def _parse_daily_cost_limit_millicents(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        daily_cost_limit_millicents = _parse_daily_cost_limit_millicents(d.pop("daily_cost_limit_millicents"))

        outbound_ai_tag_usage_item = cls(
            tag=tag,
            requests=requests,
            error_requests=error_requests,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            tokens=tokens,
            cost_millicents=cost_millicents,
            daily_cost_limit_millicents=daily_cost_limit_millicents,
        )

        outbound_ai_tag_usage_item.additional_properties = d
        return outbound_ai_tag_usage_item

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
