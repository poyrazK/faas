from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.saved_insight_request_digest import SavedInsightRequestDigest, check_saved_insight_request_digest
from ..models.saved_insight_request_kind import SavedInsightRequestKind, check_saved_insight_request_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_events_query import InsightEventsQuery
    from ..models.insight_funnel_request import InsightFunnelRequest
    from ..models.insight_retention_request import InsightRetentionRequest
    from ..models.saved_insight_alert import SavedInsightAlert


T = TypeVar("T", bound="SavedInsightRequest")


@_attrs_define
class SavedInsightRequest:
    """A saved insight: exactly the query of its kind, without from or until, over the last range_days days. A digest or
    alert needs at least one notification channel.

    """

    key: str
    kind: SavedInsightRequestKind
    name: str | Unset = UNSET
    """Display name; defaults to the key."""
    events: InsightEventsQuery | Unset = UNSET
    """A saved events insight: the event series query and the measure its value totals."""
    funnel: InsightFunnelRequest | Unset = UNSET
    """A funnel query."""
    retention: InsightRetentionRequest | Unset = UNSET
    """A retention query."""
    range_days: int | Unset = UNSET
    """Days each run covers; default 7 (28 for retention), at most the plan's analytics retention."""
    digest: SavedInsightRequestDigest | Unset = UNSET
    """Send the value to the channels at 08:00 UTC, weekly on Mondays. PagerDuty channels get no digests."""
    alert: SavedInsightAlert | Unset = UNSET
    """Fires when the saved insight's value compares to threshold; funnel and retention values are fractions."""
    channel_ids: list[UUID] | Unset = UNSET
    """The account's notification channels for digests and alerts."""

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        kind: str = self.kind

        name = self.name

        events: dict[str, Any] | Unset = UNSET
        if not isinstance(self.events, Unset):
            events = self.events.to_dict()

        funnel: dict[str, Any] | Unset = UNSET
        if not isinstance(self.funnel, Unset):
            funnel = self.funnel.to_dict()

        retention: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retention, Unset):
            retention = self.retention.to_dict()

        range_days = self.range_days

        digest: str | Unset = UNSET
        if not isinstance(self.digest, Unset):
            digest = self.digest

        alert: dict[str, Any] | Unset = UNSET
        if not isinstance(self.alert, Unset):
            alert = self.alert.to_dict()

        channel_ids: list[str] | Unset = UNSET
        if not isinstance(self.channel_ids, Unset):
            channel_ids = []
            for channel_ids_item_data in self.channel_ids:
                channel_ids_item = str(channel_ids_item_data)
                channel_ids.append(channel_ids_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "kind": kind,
            }
        )
        if name is not UNSET:
            field_dict["name"] = name
        if events is not UNSET:
            field_dict["events"] = events
        if funnel is not UNSET:
            field_dict["funnel"] = funnel
        if retention is not UNSET:
            field_dict["retention"] = retention
        if range_days is not UNSET:
            field_dict["range_days"] = range_days
        if digest is not UNSET:
            field_dict["digest"] = digest
        if alert is not UNSET:
            field_dict["alert"] = alert
        if channel_ids is not UNSET:
            field_dict["channel_ids"] = channel_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_events_query import InsightEventsQuery
        from ..models.insight_funnel_request import InsightFunnelRequest
        from ..models.insight_retention_request import InsightRetentionRequest
        from ..models.saved_insight_alert import SavedInsightAlert

        d = dict(src_dict)
        key = d.pop("key")

        kind = check_saved_insight_request_kind(d.pop("kind"))

        name = d.pop("name", UNSET)

        _events = d.pop("events", UNSET)
        events: InsightEventsQuery | Unset
        if isinstance(_events, Unset):
            events = UNSET
        else:
            events = InsightEventsQuery.from_dict(_events)

        _funnel = d.pop("funnel", UNSET)
        funnel: InsightFunnelRequest | Unset
        if isinstance(_funnel, Unset):
            funnel = UNSET
        else:
            funnel = InsightFunnelRequest.from_dict(_funnel)

        _retention = d.pop("retention", UNSET)
        retention: InsightRetentionRequest | Unset
        if isinstance(_retention, Unset):
            retention = UNSET
        else:
            retention = InsightRetentionRequest.from_dict(_retention)

        range_days = d.pop("range_days", UNSET)

        _digest = d.pop("digest", UNSET)
        digest: SavedInsightRequestDigest | Unset
        if isinstance(_digest, Unset):
            digest = UNSET
        else:
            digest = check_saved_insight_request_digest(_digest)

        _alert = d.pop("alert", UNSET)
        alert: SavedInsightAlert | Unset
        if isinstance(_alert, Unset):
            alert = UNSET
        else:
            alert = SavedInsightAlert.from_dict(_alert)

        _channel_ids = d.pop("channel_ids", UNSET)
        channel_ids: list[UUID] | Unset = UNSET
        if _channel_ids is not UNSET:
            channel_ids = []
            for channel_ids_item_data in _channel_ids:
                channel_ids_item = UUID(channel_ids_item_data)

                channel_ids.append(channel_ids_item)

        saved_insight_request = cls(
            key=key,
            kind=kind,
            name=name,
            events=events,
            funnel=funnel,
            retention=retention,
            range_days=range_days,
            digest=digest,
            alert=alert,
            channel_ids=channel_ids,
        )

        return saved_insight_request
