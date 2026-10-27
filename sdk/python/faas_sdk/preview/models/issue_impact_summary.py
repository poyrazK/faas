from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="IssueImpactSummary")


@_attrs_define
class IssueImpactSummary:
    """Observed retained occurrences, verified distinct customers, and unattributed events from the previous 24 hours."""

    identified_customers: int
    observed_events: int
    unattributed_events: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        identified_customers = self.identified_customers

        observed_events = self.observed_events

        unattributed_events = self.unattributed_events

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "identified_customers": identified_customers,
                "observed_events": observed_events,
                "unattributed_events": unattributed_events,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        identified_customers = d.pop("identified_customers")

        observed_events = d.pop("observed_events")

        unattributed_events = d.pop("unattributed_events")

        issue_impact_summary = cls(
            identified_customers=identified_customers,
            observed_events=observed_events,
            unattributed_events=unattributed_events,
        )

        issue_impact_summary.additional_properties = d
        return issue_impact_summary

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
