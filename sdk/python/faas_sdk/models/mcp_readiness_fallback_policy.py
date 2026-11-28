from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_fallback_policy_integration_key_masked import (
    MCPReadinessFallbackPolicyIntegrationKeyMasked,
    check_mcp_readiness_fallback_policy_integration_key_masked,
)
from ..models.mcp_readiness_fallback_policy_provider import (
    MCPReadinessFallbackPolicyProvider,
    check_mcp_readiness_fallback_policy_provider,
)
from ..models.mcp_readiness_fallback_policy_region import (
    MCPReadinessFallbackPolicyRegion,
    check_mcp_readiness_fallback_policy_region,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessFallbackPolicy")


@_attrs_define
class MCPReadinessFallbackPolicy:
    """Masked rule configuration for readiness incidents whose delivery has not yet been attempted."""

    version: int
    """Monotonic fallback configuration version."""
    enabled: bool
    """Whether this configuration supplies a fallback to new delivery incidents."""
    provider: MCPReadinessFallbackPolicyProvider | Unset = UNSET
    """Configured fallback provider; omitted when disabled."""
    region: MCPReadinessFallbackPolicyRegion | Unset = UNSET
    """Configured PagerDuty fallback region."""
    integration_key_masked: MCPReadinessFallbackPolicyIntegrationKeyMasked | Unset = UNSET
    """Fixed mask for the PagerDuty fallback credential."""
    webhook_url: str | Unset = UNSET
    """Configured public fallback address; omitted when disabled."""
    webhook_secret_sealed_masked: str | Unset = UNSET
    """Constant mask for the stored fallback secret; omitted when disabled."""
    after_failures: int | Unset = UNSET
    """Configured threshold of completed failed primary dispatches."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        enabled = self.enabled

        provider: str | Unset = UNSET
        if not isinstance(self.provider, Unset):
            provider = self.provider

        region: str | Unset = UNSET
        if not isinstance(self.region, Unset):
            region = self.region

        integration_key_masked: str | Unset = UNSET
        if not isinstance(self.integration_key_masked, Unset):
            integration_key_masked = self.integration_key_masked

        webhook_url = self.webhook_url

        webhook_secret_sealed_masked = self.webhook_secret_sealed_masked

        after_failures = self.after_failures

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "enabled": enabled,
            }
        )
        if provider is not UNSET:
            field_dict["provider"] = provider
        if region is not UNSET:
            field_dict["region"] = region
        if integration_key_masked is not UNSET:
            field_dict["integration_key_masked"] = integration_key_masked
        if webhook_url is not UNSET:
            field_dict["webhook_url"] = webhook_url
        if webhook_secret_sealed_masked is not UNSET:
            field_dict["webhook_secret_sealed_masked"] = webhook_secret_sealed_masked
        if after_failures is not UNSET:
            field_dict["after_failures"] = after_failures

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        enabled = d.pop("enabled")

        _provider = d.pop("provider", UNSET)
        provider: MCPReadinessFallbackPolicyProvider | Unset
        if isinstance(_provider, Unset):
            provider = UNSET
        else:
            provider = check_mcp_readiness_fallback_policy_provider(_provider)

        _region = d.pop("region", UNSET)
        region: MCPReadinessFallbackPolicyRegion | Unset
        if isinstance(_region, Unset):
            region = UNSET
        else:
            region = check_mcp_readiness_fallback_policy_region(_region)

        _integration_key_masked = d.pop("integration_key_masked", UNSET)
        integration_key_masked: MCPReadinessFallbackPolicyIntegrationKeyMasked | Unset
        if isinstance(_integration_key_masked, Unset):
            integration_key_masked = UNSET
        else:
            integration_key_masked = check_mcp_readiness_fallback_policy_integration_key_masked(_integration_key_masked)

        webhook_url = d.pop("webhook_url", UNSET)

        webhook_secret_sealed_masked = d.pop("webhook_secret_sealed_masked", UNSET)

        after_failures = d.pop("after_failures", UNSET)

        mcp_readiness_fallback_policy = cls(
            version=version,
            enabled=enabled,
            provider=provider,
            region=region,
            integration_key_masked=integration_key_masked,
            webhook_url=webhook_url,
            webhook_secret_sealed_masked=webhook_secret_sealed_masked,
            after_failures=after_failures,
        )

        mcp_readiness_fallback_policy.additional_properties = d
        return mcp_readiness_fallback_policy

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
