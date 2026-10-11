from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_recovery_notification_retry_backlog_counts_scope import (
    EventRecoveryNotificationRetryBacklogCountsScope,
    check_event_recovery_notification_retry_backlog_counts_scope,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_notification_retry_backlog_request import EventRecoveryNotificationRetryBacklogRequest
    from ..models.event_recovery_notification_retry_backlog_totals import EventRecoveryNotificationRetryBacklogTotals


T = TypeVar("T", bound="EventRecoveryNotificationRetryBacklog")


@_attrs_define
class EventRecoveryNotificationRetryBacklog:
    """App-wide notification retry evidence from a bounded job page. Counts cover scanned jobs before filtering and never
    imply whole-app totals across unseen pages.

    """

    app_id: UUID
    observed_at: datetime.datetime
    jobs_scanned: int
    counts_scope: EventRecoveryNotificationRetryBacklogCountsScope
    totals: EventRecoveryNotificationRetryBacklogTotals
    """Counts of every saved retry request in the inspected app job page before filtering. Request statuses
    partition request_count and incomplete evidence overlaps those statuses."""
    matched_count: int
    requests: list[EventRecoveryNotificationRetryBacklogRequest]
    next_cursor: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        observed_at = self.observed_at.isoformat()

        jobs_scanned = self.jobs_scanned

        counts_scope: str = self.counts_scope

        totals = self.totals.to_dict()

        matched_count = self.matched_count

        requests = []
        for requests_item_data in self.requests:
            requests_item = requests_item_data.to_dict()
            requests.append(requests_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "observed_at": observed_at,
                "jobs_scanned": jobs_scanned,
                "counts_scope": counts_scope,
                "totals": totals,
                "matched_count": matched_count,
                "requests": requests,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_retry_backlog_request import (
            EventRecoveryNotificationRetryBacklogRequest,
        )
        from ..models.event_recovery_notification_retry_backlog_totals import (
            EventRecoveryNotificationRetryBacklogTotals,
        )

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        jobs_scanned = d.pop("jobs_scanned")

        counts_scope = check_event_recovery_notification_retry_backlog_counts_scope(d.pop("counts_scope"))

        totals = EventRecoveryNotificationRetryBacklogTotals.from_dict(d.pop("totals"))

        matched_count = d.pop("matched_count")

        requests = []
        _requests = d.pop("requests")
        for requests_item_data in _requests:
            requests_item = EventRecoveryNotificationRetryBacklogRequest.from_dict(requests_item_data)

            requests.append(requests_item)

        next_cursor = d.pop("next_cursor", UNSET)

        event_recovery_notification_retry_backlog = cls(
            app_id=app_id,
            observed_at=observed_at,
            jobs_scanned=jobs_scanned,
            counts_scope=counts_scope,
            totals=totals,
            matched_count=matched_count,
            requests=requests,
            next_cursor=next_cursor,
        )

        event_recovery_notification_retry_backlog.additional_properties = d
        return event_recovery_notification_retry_backlog

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
