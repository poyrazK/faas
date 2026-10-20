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
    """Log per-call metadata (caller, route, model, status, tokens, cost, timing) for 7 days (ADR-991). Requires
    ai_provider."""
    request_log_content: bool | Unset = UNSET
    """Also keep the first 32 KiB of each logged call's request and response bodies, never headers, for 3 days and
    at most 1,000 calls per UTC day (ADR-996). Requires request_log_enabled."""
    log_export: bool | Unset = UNSET
    """Send each settled app call to the app's log drains as one JSON line of metadata, never prompts or
    completions, about 15 seconds after it settles (ADR-1340). Requires ai_provider."""
    allow_unmetered: bool | Unset = UNSET
    """Let through writes the gateway can neither meter nor knows to be free, such as realtime client secrets,
    vector stores, containers, fine-tuning jobs and context caches. They are otherwise refused with 403
    outbound_ai_endpoint_unmetered; let through, they carry X-Gregale-AI-Unmetered and their spend is outside the
    caps (ADR-1432). Requires ai_provider."""
    auto_prompt_cache: bool | Unset = UNSET
    """Add prompt-cache breakpoints to Claude requests on the Anthropic API, Vertex AI and Amazon Bedrock that
    carry none: after a system prompt or tool list seen again within five minutes, and after the last message of a
    conversation that carries earlier turns or names X-Gregale-AI-Session. Prompts under about 1,024 tokens are not
    marked; the response names the added breakpoints in X-Gregale-AI-Prompt-Cache (ADR-1437). Requires ai_provider.
   """
    user_daily_token_limit: int | None | Unset = UNSET
    """Per-end-user UTC-day token limit for calls that send X-Gregale-AI-User (ADR-994). Requires ai_provider."""
    user_daily_cost_limit_millicents: int | None | Unset = UNSET
    """Per-end-user UTC-day cost limit for calls that send X-Gregale-AI-User (ADR-994). Requires ai_provider."""
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

        request_log_content = self.request_log_content

        log_export = self.log_export

        allow_unmetered = self.allow_unmetered

        auto_prompt_cache = self.auto_prompt_cache

        user_daily_token_limit: int | None | Unset
        if isinstance(self.user_daily_token_limit, Unset):
            user_daily_token_limit = UNSET
        else:
            user_daily_token_limit = self.user_daily_token_limit

        user_daily_cost_limit_millicents: int | None | Unset
        if isinstance(self.user_daily_cost_limit_millicents, Unset):
            user_daily_cost_limit_millicents = UNSET
        else:
            user_daily_cost_limit_millicents = self.user_daily_cost_limit_millicents

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
        if request_log_content is not UNSET:
            field_dict["request_log_content"] = request_log_content
        if log_export is not UNSET:
            field_dict["log_export"] = log_export
        if allow_unmetered is not UNSET:
            field_dict["allow_unmetered"] = allow_unmetered
        if auto_prompt_cache is not UNSET:
            field_dict["auto_prompt_cache"] = auto_prompt_cache
        if user_daily_token_limit is not UNSET:
            field_dict["user_daily_token_limit"] = user_daily_token_limit
        if user_daily_cost_limit_millicents is not UNSET:
            field_dict["user_daily_cost_limit_millicents"] = user_daily_cost_limit_millicents

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

        request_log_content = d.pop("request_log_content", UNSET)

        log_export = d.pop("log_export", UNSET)

        allow_unmetered = d.pop("allow_unmetered", UNSET)

        auto_prompt_cache = d.pop("auto_prompt_cache", UNSET)

        def _parse_user_daily_token_limit(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        user_daily_token_limit = _parse_user_daily_token_limit(d.pop("user_daily_token_limit", UNSET))

        def _parse_user_daily_cost_limit_millicents(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        user_daily_cost_limit_millicents = _parse_user_daily_cost_limit_millicents(
            d.pop("user_daily_cost_limit_millicents", UNSET)
        )

        put_outbound_ai_policy_request = cls(
            ai_provider=ai_provider,
            daily_token_limit=daily_token_limit,
            daily_cost_limit_millicents=daily_cost_limit_millicents,
            monthly_cost_limit_millicents=monthly_cost_limit_millicents,
            request_log_enabled=request_log_enabled,
            request_log_content=request_log_content,
            log_export=log_export,
            allow_unmetered=allow_unmetered,
            auto_prompt_cache=auto_prompt_cache,
            user_daily_token_limit=user_daily_token_limit,
            user_daily_cost_limit_millicents=user_daily_cost_limit_millicents,
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
