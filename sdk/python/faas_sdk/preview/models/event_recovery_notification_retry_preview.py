from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.event_recovery_notification_retry_candidate import EventRecoveryNotificationRetryCandidate


T = TypeVar("T", bound="EventRecoveryNotificationRetryPreview")


@_attrs_define
class EventRecoveryNotificationRetryPreview:
    """Advisory current receiver eligibility for a retained recovery job. Admission and execution entries remain separate.
    Missing evidence cannot create a receiver or authorize replay.

    """

    job_id: UUID
    app_id: UUID
    observed_at: datetime.datetime
    counts_complete: bool
    receivers: list[EventRecoveryNotificationRetryCandidate]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        app_id = str(self.app_id)

        observed_at = self.observed_at.isoformat()

        counts_complete = self.counts_complete

        receivers = []
        for receivers_item_data in self.receivers:
            receivers_item = receivers_item_data.to_dict()
            receivers.append(receivers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job_id": job_id,
                "app_id": app_id,
                "observed_at": observed_at,
                "counts_complete": counts_complete,
                "receivers": receivers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_retry_candidate import EventRecoveryNotificationRetryCandidate

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        app_id = UUID(d.pop("app_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        counts_complete = d.pop("counts_complete")

        receivers = []
        _receivers = d.pop("receivers")
        for receivers_item_data in _receivers:
            receivers_item = EventRecoveryNotificationRetryCandidate.from_dict(receivers_item_data)

            receivers.append(receivers_item)

        event_recovery_notification_retry_preview = cls(
            job_id=job_id,
            app_id=app_id,
            observed_at=observed_at,
            counts_complete=counts_complete,
            receivers=receivers,
        )

        event_recovery_notification_retry_preview.additional_properties = d
        return event_recovery_notification_retry_preview

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
