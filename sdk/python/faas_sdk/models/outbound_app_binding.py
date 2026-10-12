from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_integration_offer import OutboundIntegrationOffer


T = TypeVar("T", bound="OutboundAppBinding")


@_attrs_define
class OutboundAppBinding:
    """An app's attachment to a managed integration."""

    integration: OutboundIntegrationOffer
    """Account-owned managed integration metadata; never contains a provider key or gateway token."""
    app_id: UUID
    allowed_methods: list[str]
    """App-specific methods, bounded by the integration ceiling."""
    allowed_path_prefixes: list[str]
    """App-specific path prefixes, bounded by the integration ceiling."""
    daily_request_limit: int | None
    """Optional per-binding daily admitted-request limit."""
    created_at: datetime.datetime
    ai_daily_token_limit: int | None | Unset = UNSET
    """This app's own UTC-day AI input plus output token limit on an AI integration (ADR-990)."""
    ai_requests_per_minute: int | None | Unset = UNSET
    """Admitted calls per UTC minute for this app (ADR-991)."""
    ai_tokens_per_minute: int | None | Unset = UNSET
    """Settled tokens plus live reservations per UTC minute for this app (ADR-991)."""
    ai_allowed_models: list[str] | None | Unset = UNSET
    """Exact model names or prefixes ending in "*" this app may call; null allows every model (ADR-991)."""
    ai_max_output_tokens: int | None | Unset = UNSET
    """Per-request output-token ceiling; added to requests that name none (ADR-991)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        integration = self.integration.to_dict()

        app_id = str(self.app_id)

        allowed_methods = self.allowed_methods

        allowed_path_prefixes = self.allowed_path_prefixes

        daily_request_limit: int | None
        daily_request_limit = self.daily_request_limit

        created_at = self.created_at.isoformat()

        ai_daily_token_limit: int | None | Unset
        if isinstance(self.ai_daily_token_limit, Unset):
            ai_daily_token_limit = UNSET
        else:
            ai_daily_token_limit = self.ai_daily_token_limit

        ai_requests_per_minute: int | None | Unset
        if isinstance(self.ai_requests_per_minute, Unset):
            ai_requests_per_minute = UNSET
        else:
            ai_requests_per_minute = self.ai_requests_per_minute

        ai_tokens_per_minute: int | None | Unset
        if isinstance(self.ai_tokens_per_minute, Unset):
            ai_tokens_per_minute = UNSET
        else:
            ai_tokens_per_minute = self.ai_tokens_per_minute

        ai_allowed_models: list[str] | None | Unset
        if isinstance(self.ai_allowed_models, Unset):
            ai_allowed_models = UNSET
        elif isinstance(self.ai_allowed_models, list):
            ai_allowed_models = self.ai_allowed_models

        else:
            ai_allowed_models = self.ai_allowed_models

        ai_max_output_tokens: int | None | Unset
        if isinstance(self.ai_max_output_tokens, Unset):
            ai_max_output_tokens = UNSET
        else:
            ai_max_output_tokens = self.ai_max_output_tokens

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "integration": integration,
                "app_id": app_id,
                "allowed_methods": allowed_methods,
                "allowed_path_prefixes": allowed_path_prefixes,
                "daily_request_limit": daily_request_limit,
                "created_at": created_at,
            }
        )
        if ai_daily_token_limit is not UNSET:
            field_dict["ai_daily_token_limit"] = ai_daily_token_limit
        if ai_requests_per_minute is not UNSET:
            field_dict["ai_requests_per_minute"] = ai_requests_per_minute
        if ai_tokens_per_minute is not UNSET:
            field_dict["ai_tokens_per_minute"] = ai_tokens_per_minute
        if ai_allowed_models is not UNSET:
            field_dict["ai_allowed_models"] = ai_allowed_models
        if ai_max_output_tokens is not UNSET:
            field_dict["ai_max_output_tokens"] = ai_max_output_tokens

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_integration_offer import OutboundIntegrationOffer

        d = dict(src_dict)
        integration = OutboundIntegrationOffer.from_dict(d.pop("integration"))

        app_id = UUID(d.pop("app_id"))

        allowed_methods = cast(list[str], d.pop("allowed_methods"))

        allowed_path_prefixes = cast(list[str], d.pop("allowed_path_prefixes"))

        def _parse_daily_request_limit(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        daily_request_limit = _parse_daily_request_limit(d.pop("daily_request_limit"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_ai_daily_token_limit(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ai_daily_token_limit = _parse_ai_daily_token_limit(d.pop("ai_daily_token_limit", UNSET))

        def _parse_ai_requests_per_minute(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ai_requests_per_minute = _parse_ai_requests_per_minute(d.pop("ai_requests_per_minute", UNSET))

        def _parse_ai_tokens_per_minute(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ai_tokens_per_minute = _parse_ai_tokens_per_minute(d.pop("ai_tokens_per_minute", UNSET))

        def _parse_ai_allowed_models(data: object) -> list[str] | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                ai_allowed_models_type_0 = cast(list[str], data)

                return ai_allowed_models_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[str] | None | Unset, data)

        ai_allowed_models = _parse_ai_allowed_models(d.pop("ai_allowed_models", UNSET))

        def _parse_ai_max_output_tokens(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        ai_max_output_tokens = _parse_ai_max_output_tokens(d.pop("ai_max_output_tokens", UNSET))

        outbound_app_binding = cls(
            integration=integration,
            app_id=app_id,
            allowed_methods=allowed_methods,
            allowed_path_prefixes=allowed_path_prefixes,
            daily_request_limit=daily_request_limit,
            created_at=created_at,
            ai_daily_token_limit=ai_daily_token_limit,
            ai_requests_per_minute=ai_requests_per_minute,
            ai_tokens_per_minute=ai_tokens_per_minute,
            ai_allowed_models=ai_allowed_models,
            ai_max_output_tokens=ai_max_output_tokens,
        )

        outbound_app_binding.additional_properties = d
        return outbound_app_binding

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
