from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.event_recovery_notification_retry_decision_summary import (
        EventRecoveryNotificationRetryDecisionSummary,
    )


T = TypeVar("T", bound="EventRecoveryNotificationRetryHistory")


@_attrs_define
class EventRecoveryNotificationRetryHistory:
    """All retained explicit retry decisions for one recovery job, newest first. The job stores at most 100 decisions and
    pruning removes this history.

    """

    job_id: UUID
    app_id: UUID
    observed_at: datetime.datetime
    decisions: list[EventRecoveryNotificationRetryDecisionSummary]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        app_id = str(self.app_id)

        observed_at = self.observed_at.isoformat()

        decisions = []
        for decisions_item_data in self.decisions:
            decisions_item = decisions_item_data.to_dict()
            decisions.append(decisions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job_id": job_id,
                "app_id": app_id,
                "observed_at": observed_at,
                "decisions": decisions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_retry_decision_summary import (
            EventRecoveryNotificationRetryDecisionSummary,
        )

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        app_id = UUID(d.pop("app_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        decisions = []
        _decisions = d.pop("decisions")
        for decisions_item_data in _decisions:
            decisions_item = EventRecoveryNotificationRetryDecisionSummary.from_dict(decisions_item_data)

            decisions.append(decisions_item)

        event_recovery_notification_retry_history = cls(
            job_id=job_id,
            app_id=app_id,
            observed_at=observed_at,
            decisions=decisions,
        )

        event_recovery_notification_retry_history.additional_properties = d
        return event_recovery_notification_retry_history

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
