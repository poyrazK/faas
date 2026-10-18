from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_pager_duty_acknowledgement_policy_destination import (
    MCPReadinessPagerDutyAcknowledgementPolicyDestination,
    check_mcp_readiness_pager_duty_acknowledgement_policy_destination,
)
from ..models.mcp_readiness_pager_duty_acknowledgement_policy_region import (
    MCPReadinessPagerDutyAcknowledgementPolicyRegion,
    check_mcp_readiness_pager_duty_acknowledgement_policy_region,
)
from ..models.mcp_readiness_pager_duty_acknowledgement_policy_signing_secret_masked import (
    MCPReadinessPagerDutyAcknowledgementPolicySigningSecretMasked,
    check_mcp_readiness_pager_duty_acknowledgement_policy_signing_secret_masked,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessPagerDutyAcknowledgementPolicy")


@_attrs_define
class MCPReadinessPagerDutyAcknowledgementPolicy:
    """Current revocable webhook binding with a masked secret and the relative signed-ingress path."""

    version: int
    enabled: bool
    webhook_path: str
    """Relative signed callback URL for this binding; explicit destination reads always return a destination-
    specific URL. Existing original callback URLs remain bound to their original destination."""
    sync_unacknowledgements: bool | Unset = False
    """Whether this destination currently accepts signed provider unacknowledgements. False or absent preserves
    acknowledgement-only behavior; true releases only matching tracked provider ownership."""
    destination: MCPReadinessPagerDutyAcknowledgementPolicyDestination | Unset = UNSET
    """Selected native PagerDuty destination"""
    fallback_version: int | Unset = UNSET
    """Captured fallback configuration version; present for a fallback binding."""
    pagerduty_version: int | Unset = UNSET
    """Native primary receiver version captured when the acknowledgement binding was saved; omitted for fallback
    binding."""
    region: MCPReadinessPagerDutyAcknowledgementPolicyRegion | Unset = UNSET
    """Regional PagerDuty receiver bound to this enabled webhook policy."""
    service_id: str | Unset = UNSET
    """Exact provider service accepted by the enabled webhook binding."""
    signing_secret_masked: MCPReadinessPagerDutyAcknowledgementPolicySigningSecretMasked | Unset = UNSET
    """Fixed mask for an enabled signing credential; no plaintext or ciphertext is returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        enabled = self.enabled

        webhook_path = self.webhook_path

        sync_unacknowledgements = self.sync_unacknowledgements

        destination: str | Unset = UNSET
        if not isinstance(self.destination, Unset):
            destination = self.destination

        fallback_version = self.fallback_version

        pagerduty_version = self.pagerduty_version

        region: str | Unset = UNSET
        if not isinstance(self.region, Unset):
            region = self.region

        service_id = self.service_id

        signing_secret_masked: str | Unset = UNSET
        if not isinstance(self.signing_secret_masked, Unset):
            signing_secret_masked = self.signing_secret_masked

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "enabled": enabled,
                "webhook_path": webhook_path,
            }
        )
        if sync_unacknowledgements is not UNSET:
            field_dict["sync_unacknowledgements"] = sync_unacknowledgements
        if destination is not UNSET:
            field_dict["destination"] = destination
        if fallback_version is not UNSET:
            field_dict["fallback_version"] = fallback_version
        if pagerduty_version is not UNSET:
            field_dict["pagerduty_version"] = pagerduty_version
        if region is not UNSET:
            field_dict["region"] = region
        if service_id is not UNSET:
            field_dict["service_id"] = service_id
        if signing_secret_masked is not UNSET:
            field_dict["signing_secret_masked"] = signing_secret_masked

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        enabled = d.pop("enabled")

        webhook_path = d.pop("webhook_path")

        sync_unacknowledgements = d.pop("sync_unacknowledgements", UNSET)

        _destination = d.pop("destination", UNSET)
        destination: MCPReadinessPagerDutyAcknowledgementPolicyDestination | Unset
        if isinstance(_destination, Unset):
            destination = UNSET
        else:
            destination = check_mcp_readiness_pager_duty_acknowledgement_policy_destination(_destination)

        fallback_version = d.pop("fallback_version", UNSET)

        pagerduty_version = d.pop("pagerduty_version", UNSET)

        _region = d.pop("region", UNSET)
        region: MCPReadinessPagerDutyAcknowledgementPolicyRegion | Unset
        if isinstance(_region, Unset):
            region = UNSET
        else:
            region = check_mcp_readiness_pager_duty_acknowledgement_policy_region(_region)

        service_id = d.pop("service_id", UNSET)

        _signing_secret_masked = d.pop("signing_secret_masked", UNSET)
        signing_secret_masked: MCPReadinessPagerDutyAcknowledgementPolicySigningSecretMasked | Unset
        if isinstance(_signing_secret_masked, Unset):
            signing_secret_masked = UNSET
        else:
            signing_secret_masked = check_mcp_readiness_pager_duty_acknowledgement_policy_signing_secret_masked(
                _signing_secret_masked
            )

        mcp_readiness_pager_duty_acknowledgement_policy = cls(
            version=version,
            enabled=enabled,
            webhook_path=webhook_path,
            sync_unacknowledgements=sync_unacknowledgements,
            destination=destination,
            fallback_version=fallback_version,
            pagerduty_version=pagerduty_version,
            region=region,
            service_id=service_id,
            signing_secret_masked=signing_secret_masked,
        )

        mcp_readiness_pager_duty_acknowledgement_policy.additional_properties = d
        return mcp_readiness_pager_duty_acknowledgement_policy

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
