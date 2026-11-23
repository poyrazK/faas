from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.set_mcp_readiness_pager_duty_acknowledgements_request_destination import (
    SetMCPReadinessPagerDutyAcknowledgementsRequestDestination,
    check_set_mcp_readiness_pager_duty_acknowledgements_request_destination,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SetMCPReadinessPagerDutyAcknowledgementsRequest")


@_attrs_define
class SetMCPReadinessPagerDutyAcknowledgementsRequest:
    """Version-fenced replacement of a PagerDuty acknowledgement webhook binding and write-only signing secret."""

    expected_version: int
    """Current selected destination acknowledgement policy version; zero denotes unconfigured."""
    enabled: bool
    """Enable signed inbound acknowledgements or immediately revoke the current signing secret."""
    sync_unacknowledgements: bool | Unset = False
    """Opt in to signed incident.unacknowledged events for this destination. Only tracked provider acknowledgements
    can be released; other destination and manual acknowledgements remain protected."""
    destination: SetMCPReadinessPagerDutyAcknowledgementsRequestDestination | Unset = UNSET
    """Native PagerDuty receiver to bind or disable. Enabling defaults to primary; disabling without a destination
    selects the original callback binding."""
    expected_fallback_version: int | Unset = UNSET
    """Required for fallback binding instead of expected_pagerduty_version; must match an enabled native PagerDuty
    fallback."""
    expected_pagerduty_version: int | Unset = UNSET
    """Current enabled native primary PagerDuty version; required for primary binding. Omit for fallback or
    disabled policies."""
    service_id: str | Unset = UNSET
    """PagerDuty service selected by the webhook subscription filter; required only when enabling."""
    signing_secret: str | Unset = UNSET
    """Exact ASCII v3 webhook subscription signing secret from PagerDuty; required when enabling and omitted when
    disabling."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        enabled = self.enabled

        sync_unacknowledgements = self.sync_unacknowledgements

        destination: str | Unset = UNSET
        if not isinstance(self.destination, Unset):
            destination = self.destination

        expected_fallback_version = self.expected_fallback_version

        expected_pagerduty_version = self.expected_pagerduty_version

        service_id = self.service_id

        signing_secret = self.signing_secret

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "enabled": enabled,
            }
        )
        if sync_unacknowledgements is not UNSET:
            field_dict["sync_unacknowledgements"] = sync_unacknowledgements
        if destination is not UNSET:
            field_dict["destination"] = destination
        if expected_fallback_version is not UNSET:
            field_dict["expected_fallback_version"] = expected_fallback_version
        if expected_pagerduty_version is not UNSET:
            field_dict["expected_pagerduty_version"] = expected_pagerduty_version
        if service_id is not UNSET:
            field_dict["service_id"] = service_id
        if signing_secret is not UNSET:
            field_dict["signing_secret"] = signing_secret

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        enabled = d.pop("enabled")

        sync_unacknowledgements = d.pop("sync_unacknowledgements", UNSET)

        _destination = d.pop("destination", UNSET)
        destination: SetMCPReadinessPagerDutyAcknowledgementsRequestDestination | Unset
        if isinstance(_destination, Unset):
            destination = UNSET
        else:
            destination = check_set_mcp_readiness_pager_duty_acknowledgements_request_destination(_destination)

        expected_fallback_version = d.pop("expected_fallback_version", UNSET)

        expected_pagerduty_version = d.pop("expected_pagerduty_version", UNSET)

        service_id = d.pop("service_id", UNSET)

        signing_secret = d.pop("signing_secret", UNSET)

        set_mcp_readiness_pager_duty_acknowledgements_request = cls(
            expected_version=expected_version,
            enabled=enabled,
            sync_unacknowledgements=sync_unacknowledgements,
            destination=destination,
            expected_fallback_version=expected_fallback_version,
            expected_pagerduty_version=expected_pagerduty_version,
            service_id=service_id,
            signing_secret=signing_secret,
        )

        return set_mcp_readiness_pager_duty_acknowledgements_request
