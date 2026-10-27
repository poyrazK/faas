from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_pager_duty_acknowledgement_event_action import (
    MCPReadinessPagerDutyAcknowledgementEventAction,
    check_mcp_readiness_pager_duty_acknowledgement_event_action,
)
from ..models.mcp_readiness_pager_duty_acknowledgement_event_destination import (
    MCPReadinessPagerDutyAcknowledgementEventDestination,
    check_mcp_readiness_pager_duty_acknowledgement_event_destination,
)
from ..models.mcp_readiness_pager_duty_acknowledgement_event_outcome import (
    MCPReadinessPagerDutyAcknowledgementEventOutcome,
    check_mcp_readiness_pager_duty_acknowledgement_event_outcome,
)
from ..models.mcp_readiness_pager_duty_acknowledgement_event_region import (
    MCPReadinessPagerDutyAcknowledgementEventRegion,
    check_mcp_readiness_pager_duty_acknowledgement_event_region,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessPagerDutyAcknowledgementEvent")


@_attrs_define
class MCPReadinessPagerDutyAcknowledgementEvent:
    """Immutable minimal receipt committed atomically with any provider acknowledgement and reminder suppression."""

    region: MCPReadinessPagerDutyAcknowledgementEventRegion
    """Authenticated receiver region retained in this provider event receipt."""
    service_id: str
    """Authenticated PagerDuty service retained in this provider event receipt."""
    id: UUID
    event_id: str
    incident_id: UUID
    provider_incident_id: str
    occurred_at: datetime.datetime
    received_at: datetime.datetime
    policy_version: int
    outcome: MCPReadinessPagerDutyAcknowledgementEventOutcome
    attention_version: int
    action: MCPReadinessPagerDutyAcknowledgementEventAction | Unset = UNSET
    """Provider decision; absent on legacy acknowledgement receipts. Release event receipts are independent per
    destination."""
    destination: MCPReadinessPagerDutyAcknowledgementEventDestination | Unset = UNSET
    """Authenticated pinned receiver; absent on legacy primary receipts."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        region: str = self.region

        service_id = self.service_id

        id = str(self.id)

        event_id = self.event_id

        incident_id = str(self.incident_id)

        provider_incident_id = self.provider_incident_id

        occurred_at = self.occurred_at.isoformat()

        received_at = self.received_at.isoformat()

        policy_version = self.policy_version

        outcome: str = self.outcome

        attention_version = self.attention_version

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        destination: str | Unset = UNSET
        if not isinstance(self.destination, Unset):
            destination = self.destination

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "region": region,
                "service_id": service_id,
                "id": id,
                "event_id": event_id,
                "incident_id": incident_id,
                "provider_incident_id": provider_incident_id,
                "occurred_at": occurred_at,
                "received_at": received_at,
                "policy_version": policy_version,
                "outcome": outcome,
                "attention_version": attention_version,
            }
        )
        if action is not UNSET:
            field_dict["action"] = action
        if destination is not UNSET:
            field_dict["destination"] = destination

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        region = check_mcp_readiness_pager_duty_acknowledgement_event_region(d.pop("region"))

        service_id = d.pop("service_id")

        id = UUID(d.pop("id"))

        event_id = d.pop("event_id")

        incident_id = UUID(d.pop("incident_id"))

        provider_incident_id = d.pop("provider_incident_id")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        policy_version = d.pop("policy_version")

        outcome = check_mcp_readiness_pager_duty_acknowledgement_event_outcome(d.pop("outcome"))

        attention_version = d.pop("attention_version")

        _action = d.pop("action", UNSET)
        action: MCPReadinessPagerDutyAcknowledgementEventAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_mcp_readiness_pager_duty_acknowledgement_event_action(_action)

        _destination = d.pop("destination", UNSET)
        destination: MCPReadinessPagerDutyAcknowledgementEventDestination | Unset
        if isinstance(_destination, Unset):
            destination = UNSET
        else:
            destination = check_mcp_readiness_pager_duty_acknowledgement_event_destination(_destination)

        mcp_readiness_pager_duty_acknowledgement_event = cls(
            region=region,
            service_id=service_id,
            id=id,
            event_id=event_id,
            incident_id=incident_id,
            provider_incident_id=provider_incident_id,
            occurred_at=occurred_at,
            received_at=received_at,
            policy_version=policy_version,
            outcome=outcome,
            attention_version=attention_version,
            action=action,
            destination=destination,
        )

        mcp_readiness_pager_duty_acknowledgement_event.additional_properties = d
        return mcp_readiness_pager_duty_acknowledgement_event

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
