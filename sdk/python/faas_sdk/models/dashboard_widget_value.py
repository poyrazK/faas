from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dashboard_widget_kind import DashboardWidgetKind, check_dashboard_widget_kind
from ..models.dashboard_widget_window import DashboardWidgetWindow, check_dashboard_widget_window
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.dashboard_point import DashboardPoint


T = TypeVar("T", bound="DashboardWidgetValue")


@_attrs_define
class DashboardWidgetValue:
    """A widget with its resolved value, or the reason it has none."""

    title: str
    kind: DashboardWidgetKind
    app: str
    """Slug of an app the account owns."""
    metric: str | Unset = UNSET
    """app_metric: request_count, error_rate_pct, latency_p50_ms, latency_p95_ms, latency_p99_ms or cold_start_pct.
    custom_metric: the pushed metric name."""
    query: str | Unset = UNSET
    """log_count: log search filters as a URL query string (source, level, status, route, deployment_id,
    request_id, trace_id, q)."""
    slo_id: UUID | Unset = UNSET
    """slo: the SLO id."""
    window: DashboardWidgetWindow | Unset = UNSET
    """Defaults to 1h; ignored by slo widgets."""
    value: float | Unset = UNSET
    """The headline number, in unit."""
    unit: str | Unset = UNSET
    """ms, %, requests, events, gauge, per second, or % budget left."""
    detail: str | Unset = UNSET
    """Context: log counts by level, SLO attainment."""
    series: list[DashboardPoint] | Unset = UNSET
    """The custom metric's points over the window."""
    error: str | Unset = UNSET
    """Why the widget has no value: an unavailable app or source, no data, or a plan gate."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        title = self.title

        kind: str = self.kind

        app = self.app

        metric = self.metric

        query = self.query

        slo_id: str | Unset = UNSET
        if not isinstance(self.slo_id, Unset):
            slo_id = str(self.slo_id)

        window: str | Unset = UNSET
        if not isinstance(self.window, Unset):
            window = self.window

        value = self.value

        unit = self.unit

        detail = self.detail

        series: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.series, Unset):
            series = []
            for series_item_data in self.series:
                series_item = series_item_data.to_dict()
                series.append(series_item)

        error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "title": title,
                "kind": kind,
                "app": app,
            }
        )
        if metric is not UNSET:
            field_dict["metric"] = metric
        if query is not UNSET:
            field_dict["query"] = query
        if slo_id is not UNSET:
            field_dict["slo_id"] = slo_id
        if window is not UNSET:
            field_dict["window"] = window
        if value is not UNSET:
            field_dict["value"] = value
        if unit is not UNSET:
            field_dict["unit"] = unit
        if detail is not UNSET:
            field_dict["detail"] = detail
        if series is not UNSET:
            field_dict["series"] = series
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dashboard_point import DashboardPoint

        d = dict(src_dict)
        title = d.pop("title")

        kind = check_dashboard_widget_kind(d.pop("kind"))

        app = d.pop("app")

        metric = d.pop("metric", UNSET)

        query = d.pop("query", UNSET)

        _slo_id = d.pop("slo_id", UNSET)
        slo_id: UUID | Unset
        if isinstance(_slo_id, Unset):
            slo_id = UNSET
        else:
            slo_id = UUID(_slo_id)

        _window = d.pop("window", UNSET)
        window: DashboardWidgetWindow | Unset
        if isinstance(_window, Unset):
            window = UNSET
        else:
            window = check_dashboard_widget_window(_window)

        value = d.pop("value", UNSET)

        unit = d.pop("unit", UNSET)

        detail = d.pop("detail", UNSET)

        _series = d.pop("series", UNSET)
        series: list[DashboardPoint] | Unset = UNSET
        if _series is not UNSET:
            series = []
            for series_item_data in _series:
                series_item = DashboardPoint.from_dict(series_item_data)

                series.append(series_item)

        error = d.pop("error", UNSET)

        dashboard_widget_value = cls(
            title=title,
            kind=kind,
            app=app,
            metric=metric,
            query=query,
            slo_id=slo_id,
            window=window,
            value=value,
            unit=unit,
            detail=detail,
            series=series,
            error=error,
        )

        dashboard_widget_value.additional_properties = d
        return dashboard_widget_value

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
