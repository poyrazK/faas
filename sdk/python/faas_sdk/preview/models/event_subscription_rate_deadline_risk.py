from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_subscription_rate_deadline_risk_reason import (
    EventSubscriptionRateDeadlineRiskReason,
    check_event_subscription_rate_deadline_risk_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventSubscriptionRateDeadlineRisk")


@_attrs_define
class EventSubscriptionRateDeadlineRisk:
    """Payload-free identity of an expired or model-at-risk sampled recipient. Absence from the sample is not evidence that
    its deadline is safe.

    """

    event_source: str
    event_id: str
    accepted_at: datetime.datetime
    deadline_at: datetime.datetime
    reason: EventSubscriptionRateDeadlineRiskReason
    modeled_admission_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        event_source = self.event_source

        event_id = self.event_id

        accepted_at = self.accepted_at.isoformat()

        deadline_at = self.deadline_at.isoformat()

        reason: str = self.reason

        modeled_admission_at: str | Unset = UNSET
        if not isinstance(self.modeled_admission_at, Unset):
            modeled_admission_at = self.modeled_admission_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_source": event_source,
                "event_id": event_id,
                "accepted_at": accepted_at,
                "deadline_at": deadline_at,
                "reason": reason,
            }
        )
        if modeled_admission_at is not UNSET:
            field_dict["modeled_admission_at"] = modeled_admission_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        reason = check_event_subscription_rate_deadline_risk_reason(d.pop("reason"))

        _modeled_admission_at = d.pop("modeled_admission_at", UNSET)
        modeled_admission_at: datetime.datetime | Unset
        if isinstance(_modeled_admission_at, Unset):
            modeled_admission_at = UNSET
        else:
            modeled_admission_at = datetime.datetime.fromisoformat(_modeled_admission_at)

        event_subscription_rate_deadline_risk = cls(
            event_source=event_source,
            event_id=event_id,
            accepted_at=accepted_at,
            deadline_at=deadline_at,
            reason=reason,
            modeled_admission_at=modeled_admission_at,
        )

        return event_subscription_rate_deadline_risk
