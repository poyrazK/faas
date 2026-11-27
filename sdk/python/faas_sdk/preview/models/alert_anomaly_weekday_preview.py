from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.alert_anomaly_preview_skipped_window import AlertAnomalyPreviewSkippedWindow


T = TypeVar("T", bound="AlertAnomalyWeekdayPreview")


@_attrs_define
class AlertAnomalyWeekdayPreview:
    """Coverage for the same-weekday candidate."""

    evaluated: int
    """Checks the candidate could evaluate, including checks using the seven-day fallback."""
    skipped_windows: list[AlertAnomalyPreviewSkippedWindow]
    fallback_at: list[datetime.datetime]
    """Covered check times where fewer than three usable matching weekdays caused the candidate to use the seven-
    day baseline. Includes any checks with insufficient seven-day history."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        evaluated = self.evaluated

        skipped_windows = []
        for skipped_windows_item_data in self.skipped_windows:
            skipped_windows_item = skipped_windows_item_data.to_dict()
            skipped_windows.append(skipped_windows_item)

        fallback_at = []
        for fallback_at_item_data in self.fallback_at:
            fallback_at_item = fallback_at_item_data.isoformat()
            fallback_at.append(fallback_at_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "evaluated": evaluated,
                "skipped_windows": skipped_windows,
                "fallback_at": fallback_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.alert_anomaly_preview_skipped_window import AlertAnomalyPreviewSkippedWindow

        d = dict(src_dict)
        evaluated = d.pop("evaluated")

        skipped_windows = []
        _skipped_windows = d.pop("skipped_windows")
        for skipped_windows_item_data in _skipped_windows:
            skipped_windows_item = AlertAnomalyPreviewSkippedWindow.from_dict(skipped_windows_item_data)

            skipped_windows.append(skipped_windows_item)

        fallback_at = []
        _fallback_at = d.pop("fallback_at")
        for fallback_at_item_data in _fallback_at:
            fallback_at_item = datetime.datetime.fromisoformat(fallback_at_item_data)

            fallback_at.append(fallback_at_item)

        alert_anomaly_weekday_preview = cls(
            evaluated=evaluated,
            skipped_windows=skipped_windows,
            fallback_at=fallback_at,
        )

        alert_anomaly_weekday_preview.additional_properties = d
        return alert_anomaly_weekday_preview

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
