from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ThrottlePreviewRow")


@_attrs_define
class ThrottlePreviewRow:
    """One row of the dry-run preview (ADR-104 amendment 5,
    issue #881 Phase 4 D1/D2). For each surviving route we
    report the count of sub-windows where the observed rate
    exceeded the candidate rps — a count of "would-have-
    rejected" requests over the window.

    """

    route: str
    candidate_rps: float
    """Echo of the candidate rps the preview evaluated against."""
    over_cap_count: float
    """Count of sub-windows in the recommendation window
    where observed rps exceeded the candidate. NaN/Inf
    from Prometheus are coerced to 0 via
    `pkg/appmetrics.SafeFloat`.
    """
    window_start: datetime.datetime
    """RFC 3339 UTC window-start the preview was evaluated against."""
    window_end: datetime.datetime
    """RFC 3339 UTC window-end the preview was evaluated against."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        candidate_rps = self.candidate_rps

        over_cap_count = self.over_cap_count

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "candidate_rps": candidate_rps,
                "over_cap_count": over_cap_count,
                "window_start": window_start,
                "window_end": window_end,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route = d.pop("route")

        candidate_rps = d.pop("candidate_rps")

        over_cap_count = d.pop("over_cap_count")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        throttle_preview_row = cls(
            route=route,
            candidate_rps=candidate_rps,
            over_cap_count=over_cap_count,
            window_start=window_start,
            window_end=window_end,
        )

        throttle_preview_row.additional_properties = d
        return throttle_preview_row

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
