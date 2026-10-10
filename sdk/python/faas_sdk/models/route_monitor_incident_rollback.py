from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_monitor_incident_rollback_reason import (
    RouteMonitorIncidentRollbackReason,
    check_route_monitor_incident_rollback_reason,
)
from ..models.route_monitor_incident_rollback_status import (
    RouteMonitorIncidentRollbackStatus,
    check_route_monitor_incident_rollback_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteMonitorIncidentRollback")


@_attrs_define
class RouteMonitorIncidentRollback:
    """The single automatic rollback decision for an incident when on_violation is rollback (ADR-845). claimed is transient
    while the checked rollback is requested.

    """

    status: RouteMonitorIncidentRollbackStatus
    decided_at: datetime.datetime
    reason: RouteMonitorIncidentRollbackReason | Unset = UNSET
    route: str | Unset = UNSET
    """First error-budget route that triggered the decision, as METHOD /path."""
    target_deployment_id: UUID | Unset = UNSET
    operation_id: UUID | Unset = UNSET
    """Checked rollback operation readable at /v1/apps/{slug}/rollbacks/{operation}."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        decided_at = self.decided_at.isoformat()

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        route = self.route

        target_deployment_id: str | Unset = UNSET
        if not isinstance(self.target_deployment_id, Unset):
            target_deployment_id = str(self.target_deployment_id)

        operation_id: str | Unset = UNSET
        if not isinstance(self.operation_id, Unset):
            operation_id = str(self.operation_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "decided_at": decided_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if route is not UNSET:
            field_dict["route"] = route
        if target_deployment_id is not UNSET:
            field_dict["target_deployment_id"] = target_deployment_id
        if operation_id is not UNSET:
            field_dict["operation_id"] = operation_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_route_monitor_incident_rollback_status(d.pop("status"))

        decided_at = datetime.datetime.fromisoformat(d.pop("decided_at"))

        _reason = d.pop("reason", UNSET)
        reason: RouteMonitorIncidentRollbackReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_route_monitor_incident_rollback_reason(_reason)

        route = d.pop("route", UNSET)

        _target_deployment_id = d.pop("target_deployment_id", UNSET)
        target_deployment_id: UUID | Unset
        if isinstance(_target_deployment_id, Unset):
            target_deployment_id = UNSET
        else:
            target_deployment_id = UUID(_target_deployment_id)

        _operation_id = d.pop("operation_id", UNSET)
        operation_id: UUID | Unset
        if isinstance(_operation_id, Unset):
            operation_id = UNSET
        else:
            operation_id = UUID(_operation_id)

        route_monitor_incident_rollback = cls(
            status=status,
            decided_at=decided_at,
            reason=reason,
            route=route,
            target_deployment_id=target_deployment_id,
            operation_id=operation_id,
        )

        route_monitor_incident_rollback.additional_properties = d
        return route_monitor_incident_rollback

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
