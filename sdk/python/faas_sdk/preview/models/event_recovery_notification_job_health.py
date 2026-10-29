from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_recovery_notification_job_health_kind import (
    EventRecoveryNotificationJobHealthKind,
    check_event_recovery_notification_job_health_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryNotificationJobHealth")


@_attrs_define
class EventRecoveryNotificationJobHealth:
    """Diagnostic notification sample. Overdue requires a known unacknowledged receiver and a capture timestamp at least
    fifteen minutes old. Missing receiver history does not prove delivery failure.

    """

    job_id: UUID
    kind: EventRecoveryNotificationJobHealthKind
    capture_status: str
    acknowledgement_status: str
    evidence_source: str
    overdue: bool
    dead: bool
    unknown: bool
    no_receivers: bool
    event: str | Unset = UNSET
    captured_at: datetime.datetime | Unset = UNSET
    unacknowledged_age_seconds: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        kind: str = self.kind

        capture_status = self.capture_status

        acknowledgement_status = self.acknowledgement_status

        evidence_source = self.evidence_source

        overdue = self.overdue

        dead = self.dead

        unknown = self.unknown

        no_receivers = self.no_receivers

        event = self.event

        captured_at: str | Unset = UNSET
        if not isinstance(self.captured_at, Unset):
            captured_at = self.captured_at.isoformat()

        unacknowledged_age_seconds = self.unacknowledged_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job_id": job_id,
                "kind": kind,
                "capture_status": capture_status,
                "acknowledgement_status": acknowledgement_status,
                "evidence_source": evidence_source,
                "overdue": overdue,
                "dead": dead,
                "unknown": unknown,
                "no_receivers": no_receivers,
            }
        )
        if event is not UNSET:
            field_dict["event"] = event
        if captured_at is not UNSET:
            field_dict["captured_at"] = captured_at
        if unacknowledged_age_seconds is not UNSET:
            field_dict["unacknowledged_age_seconds"] = unacknowledged_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        kind = check_event_recovery_notification_job_health_kind(d.pop("kind"))

        capture_status = d.pop("capture_status")

        acknowledgement_status = d.pop("acknowledgement_status")

        evidence_source = d.pop("evidence_source")

        overdue = d.pop("overdue")

        dead = d.pop("dead")

        unknown = d.pop("unknown")

        no_receivers = d.pop("no_receivers")

        event = d.pop("event", UNSET)

        _captured_at = d.pop("captured_at", UNSET)
        captured_at: datetime.datetime | Unset
        if isinstance(_captured_at, Unset):
            captured_at = UNSET
        else:
            captured_at = datetime.datetime.fromisoformat(_captured_at)

        unacknowledged_age_seconds = d.pop("unacknowledged_age_seconds", UNSET)

        event_recovery_notification_job_health = cls(
            job_id=job_id,
            kind=kind,
            capture_status=capture_status,
            acknowledgement_status=acknowledgement_status,
            evidence_source=evidence_source,
            overdue=overdue,
            dead=dead,
            unknown=unknown,
            no_receivers=no_receivers,
            event=event,
            captured_at=captured_at,
            unacknowledged_age_seconds=unacknowledged_age_seconds,
        )

        event_recovery_notification_job_health.additional_properties = d
        return event_recovery_notification_job_health

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
