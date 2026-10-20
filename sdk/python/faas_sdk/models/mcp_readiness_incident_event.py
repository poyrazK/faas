from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_readiness_incident_event_delivery_status import (
    MCPReadinessIncidentEventDeliveryStatus,
    check_mcp_readiness_incident_event_delivery_status,
)
from ..models.mcp_readiness_incident_event_event import (
    MCPReadinessIncidentEventEvent,
    check_mcp_readiness_incident_event_event,
)
from ..models.mcp_readiness_incident_event_incident_status import (
    MCPReadinessIncidentEventIncidentStatus,
    check_mcp_readiness_incident_event_incident_status,
)
from ..models.mcp_readiness_incident_event_notification_reason import (
    MCPReadinessIncidentEventNotificationReason,
    check_mcp_readiness_incident_event_notification_reason,
)
from ..models.mcp_readiness_incident_event_suppression_reason import (
    MCPReadinessIncidentEventSuppressionReason,
    check_mcp_readiness_incident_event_suppression_reason,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_readiness_delivery_routing import MCPReadinessDeliveryRouting
    from ..models.mcp_readiness_destination_delivery import MCPReadinessDestinationDelivery
    from ..models.mcp_readiness_monitor_status import MCPReadinessMonitorStatus
    from ..models.mcp_readiness_notification_receipt import MCPReadinessNotificationReceipt


T = TypeVar("T", bound="MCPReadinessIncidentEvent")


@_attrs_define
class MCPReadinessIncidentEvent:
    """Readiness incident transition or continuation with current notification status."""

    id: UUID
    """Logical event ID."""
    incident_id: UUID
    event_id: UUID
    """Stable across retries; also the signed webhook ID."""
    event: MCPReadinessIncidentEventEvent
    rule_id: UUID
    scopes: list[str]
    occurred_at: datetime.datetime
    """Logical event time"""
    incident_status: MCPReadinessIncidentEventIncidentStatus
    """Current incident state independent of notification success."""
    delivery_status: MCPReadinessIncidentEventDeliveryStatus
    attempt_count: int
    subjects: list[MCPReadinessMonitorStatus]
    destination_deliveries: list[MCPReadinessDestinationDelivery] | Unset = UNSET
    """Materialized receiver progress with independent retry and lease times."""
    delivery_routing: MCPReadinessDeliveryRouting | Unset = UNSET
    """Credential-free destination progress for one immutable readiness event. A recovery can remain pending after
    fallback succeeds while the previously informed primary is still outstanding."""
    mcp_readiness_scope: str | Unset = UNSET
    """Empty or omitted selects all environments."""
    attempted_at: datetime.datetime | Unset = UNSET
    delivery_version: int | Unset = UNSET
    """Changes on claims, completions, suppression and operator decisions."""
    last_operator_action: MCPReadinessNotificationReceipt | Unset = UNSET
    """Immutable recovery decision retained transactionally with the queue change."""
    maintenance_window_ids: list[UUID] | Unset = UNSET
    """Windows responsible for suppressing this notification."""
    suppression_reason: MCPReadinessIncidentEventSuppressionReason | Unset = UNSET
    notification_reason: MCPReadinessIncidentEventNotificationReason | Unset = UNSET
    reminder_number: int | Unset = UNSET
    """Per-incident reminder creation number; omitted for ordinary notifications."""
    next_attempt_at: datetime.datetime | Unset = UNSET
    """Next eligible delivery time. Omitted for all terminal delivery states."""
    last_delivery_id: UUID | Unset = UNSET
    last_status_code: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        incident_id = str(self.incident_id)

        event_id = str(self.event_id)

        event: str = self.event

        rule_id = str(self.rule_id)

        scopes = self.scopes

        occurred_at = self.occurred_at.isoformat()

        incident_status: str = self.incident_status

        delivery_status: str = self.delivery_status

        attempt_count = self.attempt_count

        subjects = []
        for subjects_item_data in self.subjects:
            subjects_item = subjects_item_data.to_dict()
            subjects.append(subjects_item)

        destination_deliveries: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.destination_deliveries, Unset):
            destination_deliveries = []
            for destination_deliveries_item_data in self.destination_deliveries:
                destination_deliveries_item = destination_deliveries_item_data.to_dict()
                destination_deliveries.append(destination_deliveries_item)

        delivery_routing: dict[str, Any] | Unset = UNSET
        if not isinstance(self.delivery_routing, Unset):
            delivery_routing = self.delivery_routing.to_dict()

        mcp_readiness_scope = self.mcp_readiness_scope

        attempted_at: str | Unset = UNSET
        if not isinstance(self.attempted_at, Unset):
            attempted_at = self.attempted_at.isoformat()

        delivery_version = self.delivery_version

        last_operator_action: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_operator_action, Unset):
            last_operator_action = self.last_operator_action.to_dict()

        maintenance_window_ids: list[str] | Unset = UNSET
        if not isinstance(self.maintenance_window_ids, Unset):
            maintenance_window_ids = []
            for maintenance_window_ids_item_data in self.maintenance_window_ids:
                maintenance_window_ids_item = str(maintenance_window_ids_item_data)
                maintenance_window_ids.append(maintenance_window_ids_item)

        suppression_reason: str | Unset = UNSET
        if not isinstance(self.suppression_reason, Unset):
            suppression_reason = self.suppression_reason

        notification_reason: str | Unset = UNSET
        if not isinstance(self.notification_reason, Unset):
            notification_reason = self.notification_reason

        reminder_number = self.reminder_number

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        last_delivery_id: str | Unset = UNSET
        if not isinstance(self.last_delivery_id, Unset):
            last_delivery_id = str(self.last_delivery_id)

        last_status_code = self.last_status_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "incident_id": incident_id,
                "event_id": event_id,
                "event": event,
                "rule_id": rule_id,
                "scopes": scopes,
                "occurred_at": occurred_at,
                "incident_status": incident_status,
                "delivery_status": delivery_status,
                "attempt_count": attempt_count,
                "subjects": subjects,
            }
        )
        if destination_deliveries is not UNSET:
            field_dict["destination_deliveries"] = destination_deliveries
        if delivery_routing is not UNSET:
            field_dict["delivery_routing"] = delivery_routing
        if mcp_readiness_scope is not UNSET:
            field_dict["mcp_readiness_scope"] = mcp_readiness_scope
        if attempted_at is not UNSET:
            field_dict["attempted_at"] = attempted_at
        if delivery_version is not UNSET:
            field_dict["delivery_version"] = delivery_version
        if last_operator_action is not UNSET:
            field_dict["last_operator_action"] = last_operator_action
        if maintenance_window_ids is not UNSET:
            field_dict["maintenance_window_ids"] = maintenance_window_ids
        if suppression_reason is not UNSET:
            field_dict["suppression_reason"] = suppression_reason
        if notification_reason is not UNSET:
            field_dict["notification_reason"] = notification_reason
        if reminder_number is not UNSET:
            field_dict["reminder_number"] = reminder_number
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if last_delivery_id is not UNSET:
            field_dict["last_delivery_id"] = last_delivery_id
        if last_status_code is not UNSET:
            field_dict["last_status_code"] = last_status_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_delivery_routing import MCPReadinessDeliveryRouting
        from ..models.mcp_readiness_destination_delivery import MCPReadinessDestinationDelivery
        from ..models.mcp_readiness_monitor_status import MCPReadinessMonitorStatus
        from ..models.mcp_readiness_notification_receipt import MCPReadinessNotificationReceipt

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        incident_id = UUID(d.pop("incident_id"))

        event_id = UUID(d.pop("event_id"))

        event = check_mcp_readiness_incident_event_event(d.pop("event"))

        rule_id = UUID(d.pop("rule_id"))

        scopes = cast(list[str], d.pop("scopes"))

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        incident_status = check_mcp_readiness_incident_event_incident_status(d.pop("incident_status"))

        delivery_status = check_mcp_readiness_incident_event_delivery_status(d.pop("delivery_status"))

        attempt_count = d.pop("attempt_count")

        subjects = []
        _subjects = d.pop("subjects")
        for subjects_item_data in _subjects:
            subjects_item = MCPReadinessMonitorStatus.from_dict(subjects_item_data)

            subjects.append(subjects_item)

        _destination_deliveries = d.pop("destination_deliveries", UNSET)
        destination_deliveries: list[MCPReadinessDestinationDelivery] | Unset = UNSET
        if _destination_deliveries is not UNSET:
            destination_deliveries = []
            for destination_deliveries_item_data in _destination_deliveries:
                destination_deliveries_item = MCPReadinessDestinationDelivery.from_dict(
                    destination_deliveries_item_data
                )

                destination_deliveries.append(destination_deliveries_item)

        _delivery_routing = d.pop("delivery_routing", UNSET)
        delivery_routing: MCPReadinessDeliveryRouting | Unset
        if isinstance(_delivery_routing, Unset):
            delivery_routing = UNSET
        else:
            delivery_routing = MCPReadinessDeliveryRouting.from_dict(_delivery_routing)

        mcp_readiness_scope = d.pop("mcp_readiness_scope", UNSET)

        _attempted_at = d.pop("attempted_at", UNSET)
        attempted_at: datetime.datetime | Unset
        if isinstance(_attempted_at, Unset):
            attempted_at = UNSET
        else:
            attempted_at = datetime.datetime.fromisoformat(_attempted_at)

        delivery_version = d.pop("delivery_version", UNSET)

        _last_operator_action = d.pop("last_operator_action", UNSET)
        last_operator_action: MCPReadinessNotificationReceipt | Unset
        if isinstance(_last_operator_action, Unset):
            last_operator_action = UNSET
        else:
            last_operator_action = MCPReadinessNotificationReceipt.from_dict(_last_operator_action)

        _maintenance_window_ids = d.pop("maintenance_window_ids", UNSET)
        maintenance_window_ids: list[UUID] | Unset = UNSET
        if _maintenance_window_ids is not UNSET:
            maintenance_window_ids = []
            for maintenance_window_ids_item_data in _maintenance_window_ids:
                maintenance_window_ids_item = UUID(maintenance_window_ids_item_data)

                maintenance_window_ids.append(maintenance_window_ids_item)

        _suppression_reason = d.pop("suppression_reason", UNSET)
        suppression_reason: MCPReadinessIncidentEventSuppressionReason | Unset
        if isinstance(_suppression_reason, Unset):
            suppression_reason = UNSET
        else:
            suppression_reason = check_mcp_readiness_incident_event_suppression_reason(_suppression_reason)

        _notification_reason = d.pop("notification_reason", UNSET)
        notification_reason: MCPReadinessIncidentEventNotificationReason | Unset
        if isinstance(_notification_reason, Unset):
            notification_reason = UNSET
        else:
            notification_reason = check_mcp_readiness_incident_event_notification_reason(_notification_reason)

        reminder_number = d.pop("reminder_number", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _last_delivery_id = d.pop("last_delivery_id", UNSET)
        last_delivery_id: UUID | Unset
        if isinstance(_last_delivery_id, Unset):
            last_delivery_id = UNSET
        else:
            last_delivery_id = UUID(_last_delivery_id)

        last_status_code = d.pop("last_status_code", UNSET)

        mcp_readiness_incident_event = cls(
            id=id,
            incident_id=incident_id,
            event_id=event_id,
            event=event,
            rule_id=rule_id,
            scopes=scopes,
            occurred_at=occurred_at,
            incident_status=incident_status,
            delivery_status=delivery_status,
            attempt_count=attempt_count,
            subjects=subjects,
            destination_deliveries=destination_deliveries,
            delivery_routing=delivery_routing,
            mcp_readiness_scope=mcp_readiness_scope,
            attempted_at=attempted_at,
            delivery_version=delivery_version,
            last_operator_action=last_operator_action,
            maintenance_window_ids=maintenance_window_ids,
            suppression_reason=suppression_reason,
            notification_reason=notification_reason,
            reminder_number=reminder_number,
            next_attempt_at=next_attempt_at,
            last_delivery_id=last_delivery_id,
            last_status_code=last_status_code,
        )

        mcp_readiness_incident_event.additional_properties = d
        return mcp_readiness_incident_event

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
