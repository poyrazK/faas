from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.apps_metrics_response_range import AppsMetricsResponseRange, check_apps_metrics_response_range

if TYPE_CHECKING:
    from ..models.apps_metrics_response_apps_type_0 import AppsMetricsResponseAppsType0


T = TypeVar("T", bound="AppsMetricsResponse")


@_attrs_define
class AppsMetricsResponse:
    """Account-wide per-app metrics rollup for `GET /v1/apps/metrics?range=`
    (issue #393). One call replaces N per-app
    `/v1/apps/{slug}/metrics` calls.

    `apps` is keyed by `app_slug` so the dashboard can render the
    rows without a parallel `/v1/apps` lookup. The per-app values
    are `AppMetricsResponse` — the same shape as the per-app
    endpoint emits, so the dashboard renders the rollup with one
    code path.

    `wake_p95_ms` per app is the FLEET p95 (the underlying
    `gateway_wake_latency_seconds` histogram is unlabeled — there
    is no per-app wake histogram).

    On Prometheus failure the endpoint returns 200 with `apps:
    null` and `source: "degraded: <reason>"`, matching the
    per-app contract exactly so the dashboard has one empty-state
    branch across both endpoints.

    """

    range_: AppsMetricsResponseRange
    """Time window that was queried for every per-app rollup row."""
    source: str
    """"prometheus" on success; "degraded: <reason>" otherwise."""
    as_of: datetime.datetime
    """RFC3339Nano UTC stamp at which the rollup was assembled."""
    apps: AppsMetricsResponseAppsType0 | None
    """Per-app `AppMetricsResponse`, keyed by app_slug. Null when degraded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.apps_metrics_response_apps_type_0 import AppsMetricsResponseAppsType0

        range_: str = self.range_

        source = self.source

        as_of = self.as_of.isoformat()

        apps: dict[str, Any] | None
        if isinstance(self.apps, AppsMetricsResponseAppsType0):
            apps = self.apps.to_dict()
        else:
            apps = self.apps

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "range": range_,
                "source": source,
                "as_of": as_of,
                "apps": apps,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.apps_metrics_response_apps_type_0 import AppsMetricsResponseAppsType0

        d = dict(src_dict)
        range_ = check_apps_metrics_response_range(d.pop("range"))

        source = d.pop("source")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        def _parse_apps(data: object) -> AppsMetricsResponseAppsType0 | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                apps_type_0 = AppsMetricsResponseAppsType0.from_dict(data)

                return apps_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(AppsMetricsResponseAppsType0 | None, data)

        apps = _parse_apps(d.pop("apps"))

        apps_metrics_response = cls(
            range_=range_,
            source=source,
            as_of=as_of,
            apps=apps,
        )

        apps_metrics_response.additional_properties = d
        return apps_metrics_response

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
