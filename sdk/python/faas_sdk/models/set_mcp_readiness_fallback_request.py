from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.set_mcp_readiness_fallback_request_provider import (
    SetMCPReadinessFallbackRequestProvider,
    check_set_mcp_readiness_fallback_request_provider,
)
from ..models.set_mcp_readiness_fallback_request_region import (
    SetMCPReadinessFallbackRequestRegion,
    check_set_mcp_readiness_fallback_request_region,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SetMCPReadinessFallbackRequest")


@_attrs_define
class SetMCPReadinessFallbackRequest:
    """Optimistically versioned fallback replacement. Webhook requires webhook_url and webhook_secret. PagerDuty requires
    provider=pagerduty and integration_key with optional region. Enabled policies require after_failures; disabling
    omits all destination fields.

    """

    expected_version: int
    """Current fallback policy version; zero is valid for an unconfigured rule."""
    enabled: bool
    """Whether unclaimed incidents should acquire a fallback destination."""
    provider: SetMCPReadinessFallbackRequestProvider | Unset = UNSET
    """Destination provider; omitted means webhook. Omit when disabling."""
    region: SetMCPReadinessFallbackRequestRegion | Unset = UNSET
    """PagerDuty region; defaults to US. Omit for webhook or disabled policies."""
    integration_key: str | Unset = UNSET
    """PagerDuty fallback Events API key; omit webhook fields when selecting this provider."""
    webhook_url: str | Unset = UNSET
    """Public webhook fallback receiver URL; required for webhook only and must differ from the primary webhook
    address."""
    webhook_secret: str | Unset = UNSET
    """Fallback HMAC signing material; required only when enabling or replacing a webhook policy."""
    after_failures: int | Unset = UNSET
    """Completed unsuccessful primary dispatch claims before switching this incident to fallback."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        enabled = self.enabled

        provider: str | Unset = UNSET
        if not isinstance(self.provider, Unset):
            provider = self.provider

        region: str | Unset = UNSET
        if not isinstance(self.region, Unset):
            region = self.region

        integration_key = self.integration_key

        webhook_url = self.webhook_url

        webhook_secret = self.webhook_secret

        after_failures = self.after_failures

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "enabled": enabled,
            }
        )
        if provider is not UNSET:
            field_dict["provider"] = provider
        if region is not UNSET:
            field_dict["region"] = region
        if integration_key is not UNSET:
            field_dict["integration_key"] = integration_key
        if webhook_url is not UNSET:
            field_dict["webhook_url"] = webhook_url
        if webhook_secret is not UNSET:
            field_dict["webhook_secret"] = webhook_secret
        if after_failures is not UNSET:
            field_dict["after_failures"] = after_failures

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        enabled = d.pop("enabled")

        _provider = d.pop("provider", UNSET)
        provider: SetMCPReadinessFallbackRequestProvider | Unset
        if isinstance(_provider, Unset):
            provider = UNSET
        else:
            provider = check_set_mcp_readiness_fallback_request_provider(_provider)

        _region = d.pop("region", UNSET)
        region: SetMCPReadinessFallbackRequestRegion | Unset
        if isinstance(_region, Unset):
            region = UNSET
        else:
            region = check_set_mcp_readiness_fallback_request_region(_region)

        integration_key = d.pop("integration_key", UNSET)

        webhook_url = d.pop("webhook_url", UNSET)

        webhook_secret = d.pop("webhook_secret", UNSET)

        after_failures = d.pop("after_failures", UNSET)

        set_mcp_readiness_fallback_request = cls(
            expected_version=expected_version,
            enabled=enabled,
            provider=provider,
            region=region,
            integration_key=integration_key,
            webhook_url=webhook_url,
            webhook_secret=webhook_secret,
            after_failures=after_failures,
        )

        return set_mcp_readiness_fallback_request
