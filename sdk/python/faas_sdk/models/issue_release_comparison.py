from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.issue_release_health import IssueReleaseHealth


T = TypeVar("T", bound="IssueReleaseComparison")


@_attrs_define
class IssueReleaseComparison:
    """Baseline and candidate deployment health with newly observed, recurring and resolved issue groups."""

    window_start: datetime.datetime
    window_end: datetime.datetime
    environment: str
    baseline: IssueReleaseHealth
    """Observed issue and occurrence counts for one deployment within the requested environment."""
    candidate: IssueReleaseHealth
    """Observed issue and occurrence counts for one deployment within the requested environment."""
    http_5xx_rate_delta: float | None
    """Candidate minus baseline observed HTTP 5xx rate, expressed as a fraction. Null if either has no observed
    requests."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        environment = self.environment

        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        http_5xx_rate_delta: float | None
        http_5xx_rate_delta = self.http_5xx_rate_delta

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window_start": window_start,
                "window_end": window_end,
                "environment": environment,
                "baseline": baseline,
                "candidate": candidate,
                "http_5xx_rate_delta": http_5xx_rate_delta,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_release_health import IssueReleaseHealth

        d = dict(src_dict)
        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        environment = d.pop("environment")

        baseline = IssueReleaseHealth.from_dict(d.pop("baseline"))

        candidate = IssueReleaseHealth.from_dict(d.pop("candidate"))

        def _parse_http_5xx_rate_delta(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        http_5xx_rate_delta = _parse_http_5xx_rate_delta(d.pop("http_5xx_rate_delta"))

        issue_release_comparison = cls(
            window_start=window_start,
            window_end=window_end,
            environment=environment,
            baseline=baseline,
            candidate=candidate,
            http_5xx_rate_delta=http_5xx_rate_delta,
        )

        issue_release_comparison.additional_properties = d
        return issue_release_comparison

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
