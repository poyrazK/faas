from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_health_incident_status import (
    WorkflowRecoveryHealthIncidentStatus,
    check_workflow_recovery_health_incident_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_health_notification import WorkflowRecoveryHealthNotification


T = TypeVar("T", bound="WorkflowRecoveryHealthIncident")


@_attrs_define
class WorkflowRecoveryHealthIncident:
    """Latest retained health incident per app across policy versions. Reason changes do not create repeated alerts.
    Unknown snapshots defer initial capture and cannot declare recovery. Disabling alerts closes silently; healthy or
    inactive observations close with recovery only after alert capture. Historical webhook payloads remain immutable.

    """

    episode_id: UUID
    opened_at: datetime.datetime
    candidate_since: datetime.datetime
    observed_at: datetime.datetime
    status: WorkflowRecoveryHealthIncidentStatus
    reason: str
    closed_at: datetime.datetime | Unset = UNSET
    alert_notification: WorkflowRecoveryHealthNotification | Unset = UNSET
    """Frozen health notification identity and receiver set; no_receivers is terminal for this transition and does
    not replay to late receivers."""
    recovery_notification: WorkflowRecoveryHealthNotification | Unset = UNSET
    """Frozen health notification identity and receiver set; no_receivers is terminal for this transition and does
    not replay to late receivers."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        episode_id = str(self.episode_id)

        opened_at = self.opened_at.isoformat()

        candidate_since = self.candidate_since.isoformat()

        observed_at = self.observed_at.isoformat()

        status: str = self.status

        reason = self.reason

        closed_at: str | Unset = UNSET
        if not isinstance(self.closed_at, Unset):
            closed_at = self.closed_at.isoformat()

        alert_notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.alert_notification, Unset):
            alert_notification = self.alert_notification.to_dict()

        recovery_notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery_notification, Unset):
            recovery_notification = self.recovery_notification.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "episode_id": episode_id,
                "opened_at": opened_at,
                "candidate_since": candidate_since,
                "observed_at": observed_at,
                "status": status,
                "reason": reason,
            }
        )
        if closed_at is not UNSET:
            field_dict["closed_at"] = closed_at
        if alert_notification is not UNSET:
            field_dict["alert_notification"] = alert_notification
        if recovery_notification is not UNSET:
            field_dict["recovery_notification"] = recovery_notification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_health_notification import WorkflowRecoveryHealthNotification

        d = dict(src_dict)
        episode_id = UUID(d.pop("episode_id"))

        opened_at = datetime.datetime.fromisoformat(d.pop("opened_at"))

        candidate_since = datetime.datetime.fromisoformat(d.pop("candidate_since"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        status = check_workflow_recovery_health_incident_status(d.pop("status"))

        reason = d.pop("reason")

        _closed_at = d.pop("closed_at", UNSET)
        closed_at: datetime.datetime | Unset
        if isinstance(_closed_at, Unset):
            closed_at = UNSET
        else:
            closed_at = datetime.datetime.fromisoformat(_closed_at)

        _alert_notification = d.pop("alert_notification", UNSET)
        alert_notification: WorkflowRecoveryHealthNotification | Unset
        if isinstance(_alert_notification, Unset):
            alert_notification = UNSET
        else:
            alert_notification = WorkflowRecoveryHealthNotification.from_dict(_alert_notification)

        _recovery_notification = d.pop("recovery_notification", UNSET)
        recovery_notification: WorkflowRecoveryHealthNotification | Unset
        if isinstance(_recovery_notification, Unset):
            recovery_notification = UNSET
        else:
            recovery_notification = WorkflowRecoveryHealthNotification.from_dict(_recovery_notification)

        workflow_recovery_health_incident = cls(
            episode_id=episode_id,
            opened_at=opened_at,
            candidate_since=candidate_since,
            observed_at=observed_at,
            status=status,
            reason=reason,
            closed_at=closed_at,
            alert_notification=alert_notification,
            recovery_notification=recovery_notification,
        )

        workflow_recovery_health_incident.additional_properties = d
        return workflow_recovery_health_incident

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
