from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_integration_offer_ai_provider import (
    OutboundIntegrationOfferAiProvider,
    check_outbound_integration_offer_ai_provider,
)
from ..models.outbound_integration_offer_credential_source import (
    OutboundIntegrationOfferCredentialSource,
    check_outbound_integration_offer_credential_source,
)
from ..models.outbound_integration_offer_owner_kind import (
    OutboundIntegrationOfferOwnerKind,
    check_outbound_integration_offer_owner_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_content_policy import OutboundAIContentPolicy
    from ..models.outbound_ai_online_evaluation import OutboundAIOnlineEvaluation
    from ..models.outbound_request_policy import OutboundRequestPolicy


T = TypeVar("T", bound="OutboundIntegrationOffer")


@_attrs_define
class OutboundIntegrationOffer:
    """Account-owned managed integration metadata; never contains a provider key or gateway token."""

    id: UUID
    name: str
    origin: str
    allowed_methods: list[str]
    allowed_path_prefixes: list[str]
    enabled: bool
    runs_enabled: bool
    """Whether account policy grants use by stateless Runs."""
    credential_source: OutboundIntegrationOfferCredentialSource
    """Who supplies the provider Authorization value."""
    credential_configured: bool
    """Whether the selected source currently has a credential; never reveals its value."""
    owner_kind: OutboundIntegrationOfferOwnerKind
    """Whether the integration is operator-provisioned or customer-created."""
    daily_request_limit: int | None
    """Effective per-integration UTC-day admitted-request limit; null means no configured limit."""
    request_policy: OutboundRequestPolicy
    """Effective customer-selected per-integration policy, bounded by the account plan."""
    ai_provider: OutboundIntegrationOfferAiProvider | Unset = UNSET
    """Present for AI gateway integrations (ADR-988, ADR-991). openai is the OpenAI-compatible wire format; the
    provider also selects how the managed credential is applied (header, AWS SigV4 for bedrock, Google service-
    account token for vertex) and how token usage is read."""
    ai_daily_token_limit: int | None | Unset = UNSET
    """UTC-day input plus output token limit; admission stops new calls once settled usage reaches it. null means
    no limit."""
    ai_daily_cost_limit_millicents: int | None | Unset = UNSET
    """UTC-day cost limit priced from the integration's price list (ADR-990)."""
    ai_monthly_cost_limit_millicents: int | None | Unset = UNSET
    """Calendar-month cost limit priced from the integration's price list (ADR-990)."""
    ai_request_log_enabled: bool | Unset = UNSET
    """Whether every settled AI call writes one metadata row to the request log (ADR-991)."""
    ai_request_log_content: bool | Unset = UNSET
    """Whether logged calls also keep the first 32 KiB of their request and response bodies for 3 days (ADR-996)."""
    ai_content_policy: OutboundAIContentPolicy | Unset = UNSET
    """An integration's content guardrails (ADR-997). Each detector is off, redacts its findings before the request
    reaches the provider, or blocks the request. A request containing a blocked term, compared without case, is
    blocked. The gateway checks the string values of JSON bodies and the text fields of forms."""
    ai_log_export: bool | Unset = UNSET
    """Whether each settled app call writes one metadata line to the app's log drains (ADR-1340)."""
    ai_allow_unmetered: bool | Unset = UNSET
    """Whether writes the gateway can neither meter nor knows to be free go through, outside the caps (ADR-1432)."""
    ai_auto_prompt_cache: bool | Unset = UNSET
    """Whether the gateway adds prompt-cache breakpoints to Claude requests that carry none (ADR-1437)."""
    ai_trace_max_calls: int | Unset = UNSET
    """Calls each AI trace may make, settled and in flight (ADR 20261020202035). Absent when unlimited."""
    ai_trace_max_cost_millicents: int | Unset = UNSET
    """Cost each AI trace may reach, with live reservations (ADR 20261020202035). Absent when unlimited."""
    ai_trace_max_repeated_tool_calls: int | Unset = UNSET
    """Consecutive calls of a trace whose responses may request the same tools with the same arguments (ADR
    20261020202035). Absent when unlimited."""
    ai_user_daily_token_limit: int | None | Unset = UNSET
    """UTC-day input plus output token limit for each end user named by X-Gregale-AI-User (ADR-994)."""
    ai_user_daily_cost_limit_millicents: int | None | Unset = UNSET
    """UTC-day cost limit for each end user named by X-Gregale-AI-User (ADR-994)."""
    ai_online_evaluation: OutboundAIOnlineEvaluation | Unset = UNSET
    """Score sample_percent of answered app calls from 1 to 5 with judge_model through judge_integration_id, one of
    the account's AI integrations, metered under the caller evaluation (ADR 20261021171712)."""
    ai_quality_alerts: bool | Unset = UNSET
    """Drift alerts on the integration's quality signals are on (ADR 20261021171712)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        origin = self.origin

        allowed_methods = self.allowed_methods

        allowed_path_prefixes = self.allowed_path_prefixes

        enabled = self.enabled

        runs_enabled = self.runs_enabled

        credential_source: str = self.credential_source

        credential_configured = self.credential_configured

        owner_kind: str = self.owner_kind

        daily_request_limit: int | None
        daily_request_limit = self.daily_request_limit

        request_policy = self.request_policy.to_dict()

        ai_provider: str | Unset = UNSET
        if not isinstance(self.ai_provider, Unset):
            ai_provider = self.ai_provider

        ai_daily_token_limit: int | None | Unset
        if isinstance(self.ai_daily_token_limit, Unset):
            ai_daily_token_limit = UNSET
        else:
            ai_daily_token_limit = self.ai_daily_token_limit

        ai_daily_cost_limit_millicents: int | None | Unset
        if isinstance(self.ai_daily_cost_limit_millicents, Unset):
            ai_daily_cost_limit_millicents = UNSET
        else:
            ai_daily_cost_limit_millicents = self.ai_daily_cost_limit_millicents

        ai_monthly_cost_limit_millicents: int | None | Unset
        if isinstance(self.ai_monthly_cost_limit_millicents, Unset):
            ai_monthly_cost_limit_millicents = UNSET
        else:
            ai_monthly_cost_limit_millicents = self.ai_monthly_cost_limit_millicents

        ai_request_log_enabled = self.ai_request_log_enabled

        ai_request_log_content = self.ai_request_log_content

        ai_content_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.ai_content_policy, Unset):
            ai_content_policy = self.ai_content_policy.to_dict()

        ai_log_export = self.ai_log_export

        ai_allow_unmetered = self.ai_allow_unmetered

        ai_auto_prompt_cache = self.ai_auto_prompt_cache

        ai_trace_max_calls = self.ai_trace_max_calls

        ai_trace_max_cost_millicents = self.ai_trace_max_cost_millicents

        ai_trace_max_repeated_tool_calls = self.ai_trace_max_repeated_tool_calls

        ai_user_daily_token_limit: int | None | Unset
        if isinstance(self.ai_user_daily_token_limit, Unset):
            ai_user_daily_token_limit = UNSET
        else:
            ai_user_daily_token_limit = self.ai_user_daily_token_limit

        ai_user_daily_cost_limit_millicents: int | None | Unset
        if isinstance(self.ai_user_daily_cost_limit_millicents, Unset):
            ai_user_daily_cost_limit_millicents = UNSET
        else:
            ai_user_daily_cost_limit_millicents = self.ai_user_daily_cost_limit_millicents

        ai_online_evaluation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.ai_online_evaluation, Unset):
            ai_online_evaluation = self.ai_online_evaluation.to_dict()

        ai_quality_alerts = self.ai_quality_alerts

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "origin": origin,
                "allowed_methods": allowed_methods,
                "allowed_path_prefixes": allowed_path_prefixes,
                "enabled": enabled,
                "runs_enabled": runs_enabled,
                "credential_source": credential_source,
                "credential_configured": credential_configured,
                "owner_kind": owner_kind,
                "daily_request_limit": daily_request_limit,
                "request_policy": request_policy,
            }
        )
        if ai_provider is not UNSET:
            field_dict["ai_provider"] = ai_provider
        if ai_daily_token_limit is not UNSET:
            field_dict["ai_daily_token_limit"] = ai_daily_token_limit
        if ai_daily_cost_limit_millicents is not UNSET:
            field_dict["ai_daily_cost_limit_millicents"] = ai_daily_cost_limit_millicents
        if ai_monthly_cost_limit_millicents is not UNSET:
            field_dict["ai_monthly_cost_limit_millicents"] = ai_monthly_cost_limit_millicents
        if ai_request_log_enabled is not UNSET:
            field_dict["ai_request_log_enabled"] = ai_request_log_enabled
        if ai_request_log_content is not UNSET:
            field_dict["ai_request_log_content"] = ai_request_log_content
        if ai_content_policy is not UNSET:
            field_dict["ai_content_policy"] = ai_content_policy
        if ai_log_export is not UNSET:
            field_dict["ai_log_export"] = ai_log_export
        if ai_allow_unmetered is not UNSET:
            field_dict["ai_allow_unmetered"] = ai_allow_unmetered
        if ai_auto_prompt_cache is not UNSET:
            field_dict["ai_auto_prompt_cache"] = ai_auto_prompt_cache
        if ai_trace_max_calls is not UNSET:
            field_dict["ai_trace_max_calls"] = ai_trace_max_calls
        if ai_trace_max_cost_millicents is not UNSET:
            field_dict["ai_trace_max_cost_millicents"] = ai_trace_max_cost_millicents
        if ai_trace_max_repeated_tool_calls is not UNSET:
            field_dict["ai_trace_max_repeated_tool_calls"] = ai_trace_max_repeated_tool_calls
        if ai_user_daily_token_limit is not UNSET:
            field_dict["ai_user_daily_token_limit"] = ai_user_daily_token_limit
        if ai_user_daily_cost_limit_millicents is not UNSET:
            field_dict["ai_user_daily_cost_limit_millicents"] = ai_user_daily_cost_limit_millicents
        if ai_online_evaluation is not UNSET:
            field_dict["ai_online_evaluation"] = ai_online_evaluation
        if ai_quality_alerts is not UNSET:
            field_dict["ai_quality_alerts"] = ai_quality_alerts

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_content_policy import OutboundAIContentPolicy
        from ..models.outbound_ai_online_evaluation import OutboundAIOnlineEvaluation
        from ..models.outbound_request_policy import OutboundRequestPolicy

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        origin = d.pop("origin")

        allowed_methods = cast(list[str], d.pop("allowed_methods"))

        allowed_path_prefixes = cast(list[str], d.pop("allowed_path_prefixes"))

        enabled = d.pop("enabled")

        runs_enabled = d.pop("runs_enabled")

        credential_source = check_outbound_integration_offer_credential_source(d.pop("credential_source"))

        credential_configured = d.pop("credential_configured")

        owner_kind = check_outbound_integration_offer_owner_kind(d.pop("owner_kind"))

        def _parse_daily_request_limit(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        daily_request_limit = _parse_daily_request_limit(d.pop("daily_request_limit"))

        request_policy = OutboundRequestPolicy.from_dict(d.pop("request_policy"))

        _ai_provider = d.pop("ai_provider", UNSET)
        ai_provider: OutboundIntegrationOfferAiProvider | Unset
        if isinstance(_ai_provider, Unset):
            ai_provider = UNSET
        else:
            ai_provider = check_outbound_integration_offer_ai_provider(_ai_provider)

        def _parse_ai_daily_token_limit(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ai_daily_token_limit = _parse_ai_daily_token_limit(d.pop("ai_daily_token_limit", UNSET))

        def _parse_ai_daily_cost_limit_millicents(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ai_daily_cost_limit_millicents = _parse_ai_daily_cost_limit_millicents(
            d.pop("ai_daily_cost_limit_millicents", UNSET)
        )

        def _parse_ai_monthly_cost_limit_millicents(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ai_monthly_cost_limit_millicents = _parse_ai_monthly_cost_limit_millicents(
            d.pop("ai_monthly_cost_limit_millicents", UNSET)
        )

        ai_request_log_enabled = d.pop("ai_request_log_enabled", UNSET)

        ai_request_log_content = d.pop("ai_request_log_content", UNSET)

        _ai_content_policy = d.pop("ai_content_policy", UNSET)
        ai_content_policy: OutboundAIContentPolicy | Unset
        if isinstance(_ai_content_policy, Unset):
            ai_content_policy = UNSET
        else:
            ai_content_policy = OutboundAIContentPolicy.from_dict(_ai_content_policy)

        ai_log_export = d.pop("ai_log_export", UNSET)

        ai_allow_unmetered = d.pop("ai_allow_unmetered", UNSET)

        ai_auto_prompt_cache = d.pop("ai_auto_prompt_cache", UNSET)

        ai_trace_max_calls = d.pop("ai_trace_max_calls", UNSET)

        ai_trace_max_cost_millicents = d.pop("ai_trace_max_cost_millicents", UNSET)

        ai_trace_max_repeated_tool_calls = d.pop("ai_trace_max_repeated_tool_calls", UNSET)

        def _parse_ai_user_daily_token_limit(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ai_user_daily_token_limit = _parse_ai_user_daily_token_limit(d.pop("ai_user_daily_token_limit", UNSET))

        def _parse_ai_user_daily_cost_limit_millicents(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ai_user_daily_cost_limit_millicents = _parse_ai_user_daily_cost_limit_millicents(
            d.pop("ai_user_daily_cost_limit_millicents", UNSET)
        )

        _ai_online_evaluation = d.pop("ai_online_evaluation", UNSET)
        ai_online_evaluation: OutboundAIOnlineEvaluation | Unset
        if isinstance(_ai_online_evaluation, Unset):
            ai_online_evaluation = UNSET
        else:
            ai_online_evaluation = OutboundAIOnlineEvaluation.from_dict(_ai_online_evaluation)

        ai_quality_alerts = d.pop("ai_quality_alerts", UNSET)

        outbound_integration_offer = cls(
            id=id,
            name=name,
            origin=origin,
            allowed_methods=allowed_methods,
            allowed_path_prefixes=allowed_path_prefixes,
            enabled=enabled,
            runs_enabled=runs_enabled,
            credential_source=credential_source,
            credential_configured=credential_configured,
            owner_kind=owner_kind,
            daily_request_limit=daily_request_limit,
            request_policy=request_policy,
            ai_provider=ai_provider,
            ai_daily_token_limit=ai_daily_token_limit,
            ai_daily_cost_limit_millicents=ai_daily_cost_limit_millicents,
            ai_monthly_cost_limit_millicents=ai_monthly_cost_limit_millicents,
            ai_request_log_enabled=ai_request_log_enabled,
            ai_request_log_content=ai_request_log_content,
            ai_content_policy=ai_content_policy,
            ai_log_export=ai_log_export,
            ai_allow_unmetered=ai_allow_unmetered,
            ai_auto_prompt_cache=ai_auto_prompt_cache,
            ai_trace_max_calls=ai_trace_max_calls,
            ai_trace_max_cost_millicents=ai_trace_max_cost_millicents,
            ai_trace_max_repeated_tool_calls=ai_trace_max_repeated_tool_calls,
            ai_user_daily_token_limit=ai_user_daily_token_limit,
            ai_user_daily_cost_limit_millicents=ai_user_daily_cost_limit_millicents,
            ai_online_evaluation=ai_online_evaluation,
            ai_quality_alerts=ai_quality_alerts,
        )

        outbound_integration_offer.additional_properties = d
        return outbound_integration_offer

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
