from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.put_outbound_ai_policy_request_ai_provider import (
    PutOutboundAIPolicyRequestAiProvider,
    check_put_outbound_ai_policy_request_ai_provider,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PutOutboundAIPolicyRequest")


@_attrs_define
class PutOutboundAIPolicyRequest:
    """Replace an integration's AI gateway settings. An empty ai_provider turns AI handling off and requires a null
    daily_token_limit.

    """

    ai_provider: PutOutboundAIPolicyRequestAiProvider
    daily_token_limit: int | None
    daily_cost_limit_millicents: int | None | Unset = UNSET
    monthly_cost_limit_millicents: int | None | Unset = UNSET
    request_log_enabled: bool | Unset = UNSET
    """Log per-call metadata (caller, route, model, status, tokens, cost, timing) for 7 days (ADR-984). Requires
    ai_provider."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ai_provider: str = self.ai_provider

        daily_token_limit: int | None
        daily_token_limit = self.daily_token_limit

        daily_cost_limit_millicents: int | None | Unset
        if isinstance(self.daily_cost_limit_millicents, Unset):
            daily_cost_limit_millicents = UNSET
        else:
            daily_cost_limit_millicents = self.daily_cost_limit_millicents

        monthly_cost_limit_millicents: int | None | Unset
        if isinstance(self.monthly_cost_limit_millicents, Unset):
            monthly_cost_limit_millicents = UNSET
        else:
            monthly_cost_limit_millicents = self.monthly_cost_limit_millicents

        request_log_enabled = self.request_log_enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ai_provider": ai_provider,
                "daily_token_limit": daily_token_limit,
            }
        )
        if daily_cost_limit_millicents is not UNSET:
            field_dict["daily_cost_limit_millicents"] = daily_cost_limit_millicents
        if monthly_cost_limit_millicents is not UNSET:
            field_dict["monthly_cost_limit_millicents"] = monthly_cost_limit_millicents
        if request_log_enabled is not UNSET:
            field_dict["request_log_enabled"] = request_log_enabled

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ai_provider = check_put_outbound_ai_policy_request_ai_provider(d.pop("ai_provider"))

        def _parse_daily_token_limit(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        daily_token_limit = _parse_daily_token_limit(d.pop("daily_token_limit"))

        def _parse_daily_cost_limit_millicents(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        daily_cost_limit_millicents = _parse_daily_cost_limit_millicents(d.pop("daily_cost_limit_millicents", UNSET))

        def _parse_monthly_cost_limit_millicents(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        monthly_cost_limit_millicents = _parse_monthly_cost_limit_millicents(
            d.pop("monthly_cost_limit_millicents", UNSET)
        )

        request_log_enabled = d.pop("request_log_enabled", UNSET)

        put_outbound_ai_policy_request = cls(
            ai_provider=ai_provider,
            daily_token_limit=daily_token_limit,
            daily_cost_limit_millicents=daily_cost_limit_millicents,
            monthly_cost_limit_millicents=monthly_cost_limit_millicents,
            request_log_enabled=request_log_enabled,
        )

        put_outbound_ai_policy_request.additional_properties = d
        return put_outbound_ai_policy_request

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
