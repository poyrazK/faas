from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_fanout_attempt_history_response_coverage import (
    EventFanoutAttemptHistoryResponseCoverage,
    check_event_fanout_attempt_history_response_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_fanout_attempt_response import EventFanoutAttemptResponse
    from ..models.event_fanout_history_summary_response import EventFanoutHistorySummaryResponse


T = TypeVar("T", bound="EventFanoutAttemptHistoryResponse")


@_attrs_define
class EventFanoutAttemptHistoryResponse:
    """Bounded recorded routing outcomes with durable recipient summaries. Repeated capacity waits coalesce; details have
    row and logical byte caps and thirty-day retention independent of receipt settlement. Latest outcome, failure and
    replay evidence is prioritized. Summaries describe gaps, not an exhaustive historical audit. IDs remain immutable
    and cursors remain valid after pruning. Summaries are current observations across the identity, independent of the
    detail page cursor.

    """

    app_slug: str
    event_source: str
    event_id: str
    history: list[EventFanoutAttemptResponse]
    subscription_id: UUID | Unset = UNSET
    next_before: str | Unset = UNSET
    """Opaque cursor bound to the app, event identity, and optional recipient filter."""
    coverage: EventFanoutAttemptHistoryResponseCoverage | Unset = UNSET
    """Recorded observations only; pre-migration transitions and compacted details are unavailable."""
    summaries: list[EventFanoutHistorySummaryResponse] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        event_source = self.event_source

        event_id = self.event_id

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        subscription_id: str | Unset = UNSET
        if not isinstance(self.subscription_id, Unset):
            subscription_id = str(self.subscription_id)

        next_before = self.next_before

        coverage: str | Unset = UNSET
        if not isinstance(self.coverage, Unset):
            coverage = self.coverage

        summaries: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.summaries, Unset):
            summaries = []
            for summaries_item_data in self.summaries:
                summaries_item = summaries_item_data.to_dict()
                summaries.append(summaries_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "event_source": event_source,
                "event_id": event_id,
                "history": history,
            }
        )
        if subscription_id is not UNSET:
            field_dict["subscription_id"] = subscription_id
        if next_before is not UNSET:
            field_dict["next_before"] = next_before
        if coverage is not UNSET:
            field_dict["coverage"] = coverage
        if summaries is not UNSET:
            field_dict["summaries"] = summaries

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_fanout_attempt_response import EventFanoutAttemptResponse
        from ..models.event_fanout_history_summary_response import EventFanoutHistorySummaryResponse

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = EventFanoutAttemptResponse.from_dict(history_item_data)

            history.append(history_item)

        _subscription_id = d.pop("subscription_id", UNSET)
        subscription_id: UUID | Unset
        if isinstance(_subscription_id, Unset):
            subscription_id = UNSET
        else:
            subscription_id = UUID(_subscription_id)

        next_before = d.pop("next_before", UNSET)

        _coverage = d.pop("coverage", UNSET)
        coverage: EventFanoutAttemptHistoryResponseCoverage | Unset
        if isinstance(_coverage, Unset):
            coverage = UNSET
        else:
            coverage = check_event_fanout_attempt_history_response_coverage(_coverage)

        _summaries = d.pop("summaries", UNSET)
        summaries: list[EventFanoutHistorySummaryResponse] | Unset = UNSET
        if _summaries is not UNSET:
            summaries = []
            for summaries_item_data in _summaries:
                summaries_item = EventFanoutHistorySummaryResponse.from_dict(summaries_item_data)

                summaries.append(summaries_item)

        event_fanout_attempt_history_response = cls(
            app_slug=app_slug,
            event_source=event_source,
            event_id=event_id,
            history=history,
            subscription_id=subscription_id,
            next_before=next_before,
            coverage=coverage,
            summaries=summaries,
        )

        return event_fanout_attempt_history_response
