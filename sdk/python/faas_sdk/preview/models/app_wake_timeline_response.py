from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_wake_history_page import AppWakeHistoryPage
    from ..models.app_wake_timeline_response_trigger_class_histogram import AppWakeTimelineResponseTriggerClassHistogram
    from ..models.app_wake_timeline_response_trigger_histogram import AppWakeTimelineResponseTriggerHistogram
    from ..models.wake_timeline_app import WakeTimelineApp
    from ..models.wake_timeline_json_row import WakeTimelineJSONRow


T = TypeVar("T", bound="AppWakeTimelineResponse")


@_attrs_define
class AppWakeTimelineResponse:
    """JSON mirror of the dashboard per-app wake-timeline page.
    Plan-gated Hobby+ (same code as /v1/apps/{slug}/metrics:
    plan_per_app_metrics_not_allowed).

    `trigger_histogram` is a JSON object (map[string]int) — empty
    `{}` on a fresh app, never null. The dashboard SPA must
    treat missing keys as 0 (JSON.parse() returns undefined for
    missing keys, not 0 — the render code adds the explicit
    `?? 0` fallback).

    `at_capacity_pct` is the share of `wake_count_with_meta`
    rows where the events.wake.boot_started join succeeded AND
    the at_capacity flag is true. Pre-PR-A fleet rows
    contribute to `wake_count_24h` but not the denominator.

    """

    app: WakeTimelineApp
    """Slim per-app identification embedded in `AppWakeTimelineResponse`.
    Carries only the fields the dashboard SPA needs for the
    wake-timeline header (slug + app_id). The wider
    pkg/dashboard.AppListItem type carries template-specific
    glyph/badge fields (SLO badge, StateBadge*, QuotaLabel) that
    don't belong on the wire.
    """
    wake_count_24h: int
    """Selected retained instance count; keyset_v1 counts only this page. Legacy mode examines 100 recent
    candidates before filtering."""
    wake_count_with_meta: int
    """Denominator for at_capacity_pct — count of rows where the events.wake.boot_started LEFT JOIN succeeded."""
    at_capacity_count: int
    """Numerator for at_capacity_pct."""
    at_capacity_pct: float
    """Share of meta-bearing rows admitted at the per-app MaxConcurrency ceiling."""
    trigger_histogram: AppWakeTimelineResponseTriggerHistogram
    """trigger → N count of WakeBootMeta.Trigger values across the meta-bearing rows. Empty {} on a fresh app,
    never null."""
    trigger_class_histogram: AppWakeTimelineResponseTriggerClassHistogram
    """trigger_class → N count of known user/monitor/crawler/preview_bot/unknown classifications. Empty {} on a
    fresh app, never null."""
    rows: list[WakeTimelineJSONRow]
    """Retained instance records in descending started_at order. keyset_v1 breaks ties by instance ID and filters
    before limiting; legacy mode filters 100 recent candidates."""
    as_of: datetime.datetime
    """RFC3339Nano UTC timestamp marking the JSON envelope's authoritative 'as of' instant."""
    page: AppWakeHistoryPage | Unset = UNSET
    """Live retained instance-history page. App/window filtering precedes the bound; missing start times are
    excluded. Counts describe this page only. Source success does not prove capture completeness, freshness or a
    snapshot. Instance IDs stay in opaque cursors."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app.to_dict()

        wake_count_24h = self.wake_count_24h

        wake_count_with_meta = self.wake_count_with_meta

        at_capacity_count = self.at_capacity_count

        at_capacity_pct = self.at_capacity_pct

        trigger_histogram = self.trigger_histogram.to_dict()

        trigger_class_histogram = self.trigger_class_histogram.to_dict()

        rows = []
        for rows_item_data in self.rows:
            rows_item = rows_item_data.to_dict()
            rows.append(rows_item)

        as_of = self.as_of.isoformat()

        page: dict[str, Any] | Unset = UNSET
        if not isinstance(self.page, Unset):
            page = self.page.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "wake_count_24h": wake_count_24h,
                "wake_count_with_meta": wake_count_with_meta,
                "at_capacity_count": at_capacity_count,
                "at_capacity_pct": at_capacity_pct,
                "trigger_histogram": trigger_histogram,
                "trigger_class_histogram": trigger_class_histogram,
                "rows": rows,
                "as_of": as_of,
            }
        )
        if page is not UNSET:
            field_dict["page"] = page

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_wake_history_page import AppWakeHistoryPage
        from ..models.app_wake_timeline_response_trigger_class_histogram import (
            AppWakeTimelineResponseTriggerClassHistogram,
        )
        from ..models.app_wake_timeline_response_trigger_histogram import AppWakeTimelineResponseTriggerHistogram
        from ..models.wake_timeline_app import WakeTimelineApp
        from ..models.wake_timeline_json_row import WakeTimelineJSONRow

        d = dict(src_dict)
        app = WakeTimelineApp.from_dict(d.pop("app"))

        wake_count_24h = d.pop("wake_count_24h")

        wake_count_with_meta = d.pop("wake_count_with_meta")

        at_capacity_count = d.pop("at_capacity_count")

        at_capacity_pct = d.pop("at_capacity_pct")

        trigger_histogram = AppWakeTimelineResponseTriggerHistogram.from_dict(d.pop("trigger_histogram"))

        trigger_class_histogram = AppWakeTimelineResponseTriggerClassHistogram.from_dict(
            d.pop("trigger_class_histogram")
        )

        rows = []
        _rows = d.pop("rows")
        for rows_item_data in _rows:
            rows_item = WakeTimelineJSONRow.from_dict(rows_item_data)

            rows.append(rows_item)

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        _page = d.pop("page", UNSET)
        page: AppWakeHistoryPage | Unset
        if isinstance(_page, Unset):
            page = UNSET
        else:
            page = AppWakeHistoryPage.from_dict(_page)

        app_wake_timeline_response = cls(
            app=app,
            wake_count_24h=wake_count_24h,
            wake_count_with_meta=wake_count_with_meta,
            at_capacity_count=at_capacity_count,
            at_capacity_pct=at_capacity_pct,
            trigger_histogram=trigger_histogram,
            trigger_class_histogram=trigger_class_histogram,
            rows=rows,
            as_of=as_of,
            page=page,
        )

        app_wake_timeline_response.additional_properties = d
        return app_wake_timeline_response

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
