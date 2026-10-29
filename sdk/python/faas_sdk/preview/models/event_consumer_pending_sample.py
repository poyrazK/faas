from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_consumer_pending_sample_coverage import (
    EventConsumerPendingSampleCoverage,
    check_event_consumer_pending_sample_coverage,
)

if TYPE_CHECKING:
    from ..models.event_backlog_recipient import EventBacklogRecipient
    from ..models.event_consumer_pending_sample_waiting_reasons import EventConsumerPendingSampleWaitingReasons


T = TypeVar("T", bound="EventConsumerPendingSample")


@_attrs_define
class EventConsumerPendingSample:
    """Up to 50 oldest pending application recipients, ordered by acceptance time with stable outbox and subscription ties.
    Waiting reason counts cover only these recipients.

    """

    observed_at: datetime.datetime
    coverage: EventConsumerPendingSampleCoverage
    truncated: bool
    """More pending recipients existed in this read."""
    waiting_reasons: EventConsumerPendingSampleWaitingReasons
    recipients: list[EventBacklogRecipient]

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        coverage: str = self.coverage

        truncated = self.truncated

        waiting_reasons = self.waiting_reasons.to_dict()

        recipients = []
        for recipients_item_data in self.recipients:
            recipients_item = recipients_item_data.to_dict()
            recipients.append(recipients_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observed_at": observed_at,
                "coverage": coverage,
                "truncated": truncated,
                "waiting_reasons": waiting_reasons,
                "recipients": recipients,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_backlog_recipient import EventBacklogRecipient
        from ..models.event_consumer_pending_sample_waiting_reasons import EventConsumerPendingSampleWaitingReasons

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        coverage = check_event_consumer_pending_sample_coverage(d.pop("coverage"))

        truncated = d.pop("truncated")

        waiting_reasons = EventConsumerPendingSampleWaitingReasons.from_dict(d.pop("waiting_reasons"))

        recipients = []
        _recipients = d.pop("recipients")
        for recipients_item_data in _recipients:
            recipients_item = EventBacklogRecipient.from_dict(recipients_item_data)

            recipients.append(recipients_item)

        event_consumer_pending_sample = cls(
            observed_at=observed_at,
            coverage=coverage,
            truncated=truncated,
            waiting_reasons=waiting_reasons,
            recipients=recipients,
        )

        return event_consumer_pending_sample
