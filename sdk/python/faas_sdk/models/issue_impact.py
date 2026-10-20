from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="IssueImpact")


@_attrs_define
class IssueImpact:
    """Observed retained customer impact within an explicit time window; unknown identity remains unattributed."""

    window_start: datetime.datetime
    window_end: datetime.datetime
    identified_customers: int
    observed_events: int
    unattributed_events: int
    coverage: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        identified_customers = self.identified_customers

        observed_events = self.observed_events

        unattributed_events = self.unattributed_events

        coverage = self.coverage

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window_start": window_start,
                "window_end": window_end,
                "identified_customers": identified_customers,
                "observed_events": observed_events,
                "unattributed_events": unattributed_events,
                "coverage": coverage,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        identified_customers = d.pop("identified_customers")

        observed_events = d.pop("observed_events")

        unattributed_events = d.pop("unattributed_events")

        coverage = d.pop("coverage")

        issue_impact = cls(
            window_start=window_start,
            window_end=window_end,
            identified_customers=identified_customers,
            observed_events=observed_events,
            unattributed_events=unattributed_events,
            coverage=coverage,
        )

        issue_impact.additional_properties = d
        return issue_impact

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
