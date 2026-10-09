from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.custom_metric_series_response_range import (
    CustomMetricSeriesResponseRange,
    check_custom_metric_series_response_range,
)

if TYPE_CHECKING:
    from ..models.custom_metric_series_point import CustomMetricSeriesPoint


T = TypeVar("T", bound="CustomMetricSeriesResponse")


@_attrs_define
class CustomMetricSeriesResponse:
    """ADR-745 history of one pushed custom metric. Degraded responses carry null points."""

    app_id: str
    name: str
    range_: CustomMetricSeriesResponseRange
    step: str
    """Prometheus step between points."""
    source: str
    """"prometheus" or "degraded: <reason>"."""
    points: list[CustomMetricSeriesPoint] | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        name = self.name

        range_: str = self.range_

        step = self.step

        source = self.source

        points: list[dict[str, Any]] | None
        if isinstance(self.points, list):
            points = []
            for points_type_0_item_data in self.points:
                points_type_0_item = points_type_0_item_data.to_dict()
                points.append(points_type_0_item)

        else:
            points = self.points

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "name": name,
                "range": range_,
                "step": step,
                "source": source,
                "points": points,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.custom_metric_series_point import CustomMetricSeriesPoint

        d = dict(src_dict)
        app_id = d.pop("app_id")

        name = d.pop("name")

        range_ = check_custom_metric_series_response_range(d.pop("range"))

        step = d.pop("step")

        source = d.pop("source")

        def _parse_points(data: object) -> list[CustomMetricSeriesPoint] | None:
            if data is None:
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                points_type_0 = []
                _points_type_0 = data
                for points_type_0_item_data in _points_type_0:
                    points_type_0_item = CustomMetricSeriesPoint.from_dict(points_type_0_item_data)

                    points_type_0.append(points_type_0_item)

                return points_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(list[CustomMetricSeriesPoint] | None, data)

        points = _parse_points(d.pop("points"))

        custom_metric_series_response = cls(
            app_id=app_id,
            name=name,
            range_=range_,
            step=step,
            source=source,
            points=points,
        )

        custom_metric_series_response.additional_properties = d
        return custom_metric_series_response

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
