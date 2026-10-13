from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundAIUserUsageItem")


@_attrs_define
class OutboundAIUserUsageItem:
    """One end user's UTC-day AI usage on an integration; tokens is input plus output, the amount the per-user token limit
    counts.

    """

    end_user: str
    """X-Gregale-AI-User value; "~other" collects end users beyond the daily cap."""
    requests: int
    error_requests: int
    input_tokens: int
    output_tokens: int
    tokens: int
    cost_millicents: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        end_user = self.end_user

        requests = self.requests

        error_requests = self.error_requests

        input_tokens = self.input_tokens

        output_tokens = self.output_tokens

        tokens = self.tokens

        cost_millicents = self.cost_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "end_user": end_user,
                "requests": requests,
                "error_requests": error_requests,
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "tokens": tokens,
                "cost_millicents": cost_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        end_user = d.pop("end_user")

        requests = d.pop("requests")

        error_requests = d.pop("error_requests")

        input_tokens = d.pop("input_tokens")

        output_tokens = d.pop("output_tokens")

        tokens = d.pop("tokens")

        cost_millicents = d.pop("cost_millicents")

        outbound_ai_user_usage_item = cls(
            end_user=end_user,
            requests=requests,
            error_requests=error_requests,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            tokens=tokens,
            cost_millicents=cost_millicents,
        )

        outbound_ai_user_usage_item.additional_properties = d
        return outbound_ai_user_usage_item

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
