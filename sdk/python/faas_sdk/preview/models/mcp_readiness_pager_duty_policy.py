from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_pager_duty_policy_integration_key_masked import (
    MCPReadinessPagerDutyPolicyIntegrationKeyMasked,
    check_mcp_readiness_pager_duty_policy_integration_key_masked,
)
from ..models.mcp_readiness_pager_duty_policy_region import (
    MCPReadinessPagerDutyPolicyRegion,
    check_mcp_readiness_pager_duty_policy_region,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessPagerDutyPolicy")


@_attrs_define
class MCPReadinessPagerDutyPolicy:
    """Masked native PagerDuty settings without plaintext, ciphertext or receiver fingerprint."""

    version: int
    """Monotonic PagerDuty receiver policy version; zero denotes webhook primary without native configuration."""
    enabled: bool
    region: MCPReadinessPagerDutyPolicyRegion
    """Effective Events API region; US for unconfigured or disabled policies."""
    integration_key_masked: MCPReadinessPagerDutyPolicyIntegrationKeyMasked | Unset = UNSET
    """Fixed mask returned only for an enabled PagerDuty integration."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        enabled = self.enabled

        region: str = self.region

        integration_key_masked: str | Unset = UNSET
        if not isinstance(self.integration_key_masked, Unset):
            integration_key_masked = self.integration_key_masked

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "enabled": enabled,
                "region": region,
            }
        )
        if integration_key_masked is not UNSET:
            field_dict["integration_key_masked"] = integration_key_masked

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        enabled = d.pop("enabled")

        region = check_mcp_readiness_pager_duty_policy_region(d.pop("region"))

        _integration_key_masked = d.pop("integration_key_masked", UNSET)
        integration_key_masked: MCPReadinessPagerDutyPolicyIntegrationKeyMasked | Unset
        if isinstance(_integration_key_masked, Unset):
            integration_key_masked = UNSET
        else:
            integration_key_masked = check_mcp_readiness_pager_duty_policy_integration_key_masked(
                _integration_key_masked
            )

        mcp_readiness_pager_duty_policy = cls(
            version=version,
            enabled=enabled,
            region=region,
            integration_key_masked=integration_key_masked,
        )

        mcp_readiness_pager_duty_policy.additional_properties = d
        return mcp_readiness_pager_duty_policy

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
