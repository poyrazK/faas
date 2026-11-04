from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_subscription_deadline_window_coverage import (
    EventSubscriptionDeadlineWindowCoverage,
    check_event_subscription_deadline_window_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_subscription_deadline_window_expired_waiting_reasons import (
        EventSubscriptionDeadlineWindowExpiredWaitingReasons,
    )
    from ..models.event_subscription_deadline_window_upcoming_waiting_reasons import (
        EventSubscriptionDeadlineWindowUpcomingWaitingReasons,
    )
    from ..models.event_subscription_pending_summary import EventSubscriptionPendingSummary


T = TypeVar("T", bound="EventSubscriptionDeadlineWindow")


@_attrs_define
class EventSubscriptionDeadlineWindow:
    """Complete, disjoint deadline cohorts with the full pending summary from one read-only snapshot. Upcoming means
    observed_at < deadline < window_end_at; expired means deadline <= observed_at. Incomplete summary coverage
    invalidates all counts for alert confirmation.

    """

    coverage: EventSubscriptionDeadlineWindowCoverage
    summary: EventSubscriptionPendingSummary
    """Full retained captured pending application-recipient counts from one read-only snapshot. No payloads,
    identities or modeled completion estimates. Incomplete evidence cannot establish health or recovery."""
    within_millis: int
    window_end_at: datetime.datetime
    upcoming_recipients: int
    upcoming_waiting_reasons: EventSubscriptionDeadlineWindowUpcomingWaitingReasons
    """Exact partition of upcoming_recipients over every known backlog waiting reason, including explicit zero
    keys. Labels do not establish unique causes."""
    expired_waiting_reasons: EventSubscriptionDeadlineWindowExpiredWaitingReasons
    """Exact partition of summary.already_expired_count over every known backlog waiting reason, including explicit
    zero keys."""
    earliest_upcoming_deadline_at: datetime.datetime | Unset = UNSET
    """Omitted exactly when upcoming_recipients is zero."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        summary = self.summary.to_dict()

        within_millis = self.within_millis

        window_end_at = self.window_end_at.isoformat()

        upcoming_recipients = self.upcoming_recipients

        upcoming_waiting_reasons = self.upcoming_waiting_reasons.to_dict()

        expired_waiting_reasons = self.expired_waiting_reasons.to_dict()

        earliest_upcoming_deadline_at: str | Unset = UNSET
        if not isinstance(self.earliest_upcoming_deadline_at, Unset):
            earliest_upcoming_deadline_at = self.earliest_upcoming_deadline_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "coverage": coverage,
                "summary": summary,
                "within_millis": within_millis,
                "window_end_at": window_end_at,
                "upcoming_recipients": upcoming_recipients,
                "upcoming_waiting_reasons": upcoming_waiting_reasons,
                "expired_waiting_reasons": expired_waiting_reasons,
            }
        )
        if earliest_upcoming_deadline_at is not UNSET:
            field_dict["earliest_upcoming_deadline_at"] = earliest_upcoming_deadline_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_deadline_window_expired_waiting_reasons import (
            EventSubscriptionDeadlineWindowExpiredWaitingReasons,
        )
        from ..models.event_subscription_deadline_window_upcoming_waiting_reasons import (
            EventSubscriptionDeadlineWindowUpcomingWaitingReasons,
        )
        from ..models.event_subscription_pending_summary import EventSubscriptionPendingSummary

        d = dict(src_dict)
        coverage = check_event_subscription_deadline_window_coverage(d.pop("coverage"))

        summary = EventSubscriptionPendingSummary.from_dict(d.pop("summary"))

        within_millis = d.pop("within_millis")

        window_end_at = datetime.datetime.fromisoformat(d.pop("window_end_at"))

        upcoming_recipients = d.pop("upcoming_recipients")

        upcoming_waiting_reasons = EventSubscriptionDeadlineWindowUpcomingWaitingReasons.from_dict(
            d.pop("upcoming_waiting_reasons")
        )

        expired_waiting_reasons = EventSubscriptionDeadlineWindowExpiredWaitingReasons.from_dict(
            d.pop("expired_waiting_reasons")
        )

        _earliest_upcoming_deadline_at = d.pop("earliest_upcoming_deadline_at", UNSET)
        earliest_upcoming_deadline_at: datetime.datetime | Unset
        if isinstance(_earliest_upcoming_deadline_at, Unset):
            earliest_upcoming_deadline_at = UNSET
        else:
            earliest_upcoming_deadline_at = datetime.datetime.fromisoformat(_earliest_upcoming_deadline_at)

        event_subscription_deadline_window = cls(
            coverage=coverage,
            summary=summary,
            within_millis=within_millis,
            window_end_at=window_end_at,
            upcoming_recipients=upcoming_recipients,
            upcoming_waiting_reasons=upcoming_waiting_reasons,
            expired_waiting_reasons=expired_waiting_reasons,
            earliest_upcoming_deadline_at=earliest_upcoming_deadline_at,
        )

        event_subscription_deadline_window.additional_properties = d
        return event_subscription_deadline_window

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
