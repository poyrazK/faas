from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.set_mcp_readiness_pager_duty_request_region import (
    SetMCPReadinessPagerDutyRequestRegion,
    check_set_mcp_readiness_pager_duty_request_region,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SetMCPReadinessPagerDutyRequest")


@_attrs_define
class SetMCPReadinessPagerDutyRequest:
    """Version-fenced PagerDuty primary receiver configuration with write-only credentials."""

    expected_version: int
    """Current native PagerDuty policy version; zero is valid for first configuration."""
    enabled: bool
    """Select PagerDuty primary for incidents not yet attempted; false restores webhook primary."""
    integration_key: str | Unset = UNSET
    """Events API v2 key for the selected region; required when enabling and omitted when disabling."""
    region: SetMCPReadinessPagerDutyRequestRegion | Unset = UNSET
    """Events API region for this replacement configuration; defaults to US when omitted. Omit when disabling."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        enabled = self.enabled

        integration_key = self.integration_key

        region: str | Unset = UNSET
        if not isinstance(self.region, Unset):
            region = self.region

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "enabled": enabled,
            }
        )
        if integration_key is not UNSET:
            field_dict["integration_key"] = integration_key
        if region is not UNSET:
            field_dict["region"] = region

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        enabled = d.pop("enabled")

        integration_key = d.pop("integration_key", UNSET)

        _region = d.pop("region", UNSET)
        region: SetMCPReadinessPagerDutyRequestRegion | Unset
        if isinstance(_region, Unset):
            region = UNSET
        else:
            region = check_set_mcp_readiness_pager_duty_request_region(_region)

        set_mcp_readiness_pager_duty_request = cls(
            expected_version=expected_version,
            enabled=enabled,
            integration_key=integration_key,
            region=region,
        )

        return set_mcp_readiness_pager_duty_request
