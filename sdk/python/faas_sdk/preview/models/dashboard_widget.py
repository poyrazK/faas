from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dashboard_widget_kind import DashboardWidgetKind, check_dashboard_widget_kind
from ..models.dashboard_widget_window import DashboardWidgetWindow, check_dashboard_widget_window
from ..types import UNSET, Unset

T = TypeVar("T", bound="DashboardWidget")


@_attrs_define
class DashboardWidget:
    """One typed reference to a signal (ADR-20261022173833). app_metric and custom_metric use metric, log_count uses query,
    slo uses slo_id.

    """

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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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

        dashboard_widget = cls(
            title=title,
            kind=kind,
            app=app,
            metric=metric,
            query=query,
            slo_id=slo_id,
            window=window,
        )

        dashboard_widget.additional_properties = d
        return dashboard_widget

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
