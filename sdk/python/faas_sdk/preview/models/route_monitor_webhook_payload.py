from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_webhook_payload_status import (
    RouteMonitorWebhookPayloadStatus,
    check_route_monitor_webhook_payload_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_customer_impact import RouteMonitorCustomerImpact
    from ..models.route_monitor_webhook_escalation import RouteMonitorWebhookEscalation


T = TypeVar("T", bound="RouteMonitorWebhookPayload")


@_attrs_define
class RouteMonitorWebhookPayload:
    """Incident lifecycle or escalation metadata and optional aggregate customer-impact counts with an authenticated saved
    incident path. Excludes customer IDs and request data.

    """

    version: int
    app_id: UUID
    deployment_id: UUID
    incident_id: UUID
    revision: int
    status: RouteMonitorWebhookPayloadStatus
    checked_at: datetime.datetime
    incident_path: str
    transition_id: UUID | Unset = UNSET
    """Stable identifier for one escalation transition; omitted for incident open and recovery events."""
    customer_impact: RouteMonitorCustomerImpact | Unset = UNSET
    """Aggregate observed request-time identity counts; never includes customer IDs."""
    escalation: RouteMonitorWebhookEscalation | Unset = UNSET
    """Counts newly violated route and signal budgets since the prior incident evaluation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        incident_id = str(self.incident_id)

        revision = self.revision

        status: str = self.status

        checked_at = self.checked_at.isoformat()

        incident_path = self.incident_path

        transition_id: str | Unset = UNSET
        if not isinstance(self.transition_id, Unset):
            transition_id = str(self.transition_id)

        customer_impact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.customer_impact, Unset):
            customer_impact = self.customer_impact.to_dict()

        escalation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.escalation, Unset):
            escalation = self.escalation.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "incident_id": incident_id,
                "revision": revision,
                "status": status,
                "checked_at": checked_at,
                "incident_path": incident_path,
            }
        )
        if transition_id is not UNSET:
            field_dict["transition_id"] = transition_id
        if customer_impact is not UNSET:
            field_dict["customer_impact"] = customer_impact
        if escalation is not UNSET:
            field_dict["escalation"] = escalation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_customer_impact import RouteMonitorCustomerImpact
        from ..models.route_monitor_webhook_escalation import RouteMonitorWebhookEscalation

        d = dict(src_dict)
        version = d.pop("version")

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        incident_id = UUID(d.pop("incident_id"))

        revision = d.pop("revision")

        status = check_route_monitor_webhook_payload_status(d.pop("status"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        incident_path = d.pop("incident_path")

        _transition_id = d.pop("transition_id", UNSET)
        transition_id: UUID | Unset
        if isinstance(_transition_id, Unset):
            transition_id = UNSET
        else:
            transition_id = UUID(_transition_id)

        _customer_impact = d.pop("customer_impact", UNSET)
        customer_impact: RouteMonitorCustomerImpact | Unset
        if isinstance(_customer_impact, Unset):
            customer_impact = UNSET
        else:
            customer_impact = RouteMonitorCustomerImpact.from_dict(_customer_impact)

        _escalation = d.pop("escalation", UNSET)
        escalation: RouteMonitorWebhookEscalation | Unset
        if isinstance(_escalation, Unset):
            escalation = UNSET
        else:
            escalation = RouteMonitorWebhookEscalation.from_dict(_escalation)

        route_monitor_webhook_payload = cls(
            version=version,
            app_id=app_id,
            deployment_id=deployment_id,
            incident_id=incident_id,
            revision=revision,
            status=status,
            checked_at=checked_at,
            incident_path=incident_path,
            transition_id=transition_id,
            customer_impact=customer_impact,
            escalation=escalation,
        )

        route_monitor_webhook_payload.additional_properties = d
        return route_monitor_webhook_payload

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
