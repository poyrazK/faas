from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_pager_duty_acknowledgement_hold_destination import (
    MCPReadinessPagerDutyAcknowledgementHoldDestination,
    check_mcp_readiness_pager_duty_acknowledgement_hold_destination,
)
from ..models.mcp_readiness_pager_duty_acknowledgement_hold_region import (
    MCPReadinessPagerDutyAcknowledgementHoldRegion,
    check_mcp_readiness_pager_duty_acknowledgement_hold_region,
)

T = TypeVar("T", bound="MCPReadinessPagerDutyAcknowledgementHold")


@_attrs_define
class MCPReadinessPagerDutyAcknowledgementHold:
    """Minimal ownership of an active provider acknowledgement; no credentials or integration fingerprints are exposed."""

    destination: MCPReadinessPagerDutyAcknowledgementHoldDestination
    provider_incident_id: str
    provider_event_id: str
    service_id: str
    region: MCPReadinessPagerDutyAcknowledgementHoldRegion
    occurred_at: datetime.datetime
    received_at: datetime.datetime
    policy_version: int
    release_sync_enabled: bool
    """False requires local release; includes acknowledgement-only bindings and ambiguous ownership."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        destination: str = self.destination

        provider_incident_id = self.provider_incident_id

        provider_event_id = self.provider_event_id

        service_id = self.service_id

        region: str = self.region

        occurred_at = self.occurred_at.isoformat()

        received_at = self.received_at.isoformat()

        policy_version = self.policy_version

        release_sync_enabled = self.release_sync_enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "destination": destination,
                "provider_incident_id": provider_incident_id,
                "provider_event_id": provider_event_id,
                "service_id": service_id,
                "region": region,
                "occurred_at": occurred_at,
                "received_at": received_at,
                "policy_version": policy_version,
                "release_sync_enabled": release_sync_enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        destination = check_mcp_readiness_pager_duty_acknowledgement_hold_destination(d.pop("destination"))

        provider_incident_id = d.pop("provider_incident_id")

        provider_event_id = d.pop("provider_event_id")

        service_id = d.pop("service_id")

        region = check_mcp_readiness_pager_duty_acknowledgement_hold_region(d.pop("region"))

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        policy_version = d.pop("policy_version")

        release_sync_enabled = d.pop("release_sync_enabled")

        mcp_readiness_pager_duty_acknowledgement_hold = cls(
            destination=destination,
            provider_incident_id=provider_incident_id,
            provider_event_id=provider_event_id,
            service_id=service_id,
            region=region,
            occurred_at=occurred_at,
            received_at=received_at,
            policy_version=policy_version,
            release_sync_enabled=release_sync_enabled,
        )

        mcp_readiness_pager_duty_acknowledgement_hold.additional_properties = d
        return mcp_readiness_pager_duty_acknowledgement_hold

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
