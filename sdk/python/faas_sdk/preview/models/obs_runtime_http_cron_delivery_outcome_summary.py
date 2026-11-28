from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_http_cron_delivery_name import (
    ObsRuntimeHTTPCronDeliveryName,
    check_obs_runtime_http_cron_delivery_name,
)

T = TypeVar("T", bound="ObsRuntimeHTTPCronDeliveryOutcomeSummary")


@_attrs_define
class ObsRuntimeHTTPCronDeliveryOutcomeSummary:
    """Full-set target classifications for one ordered HTTP delivery outcome, independent of preview clipping. A later
    quiet sample does not erase an earlier increase from retained progress.

    """

    outcome: ObsRuntimeHTTPCronDeliveryName
    """Closed producer outcome. success is a settled successful attempt; retry is a retry disposition without a
    final result; app_error is an application-error classification. failed and dead_letter remain distinct delivery-
    failure classifications. Scheduled fires and manual runs share this metric."""
    increased: int
    unchanged: int
    baseline: int
    unknown: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        increased = self.increased

        unchanged = self.unchanged

        baseline = self.baseline

        unknown = self.unknown

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "outcome": outcome,
                "increased": increased,
                "unchanged": unchanged,
                "baseline": baseline,
                "unknown": unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outcome = check_obs_runtime_http_cron_delivery_name(d.pop("outcome"))

        increased = d.pop("increased")

        unchanged = d.pop("unchanged")

        baseline = d.pop("baseline")

        unknown = d.pop("unknown")

        obs_runtime_http_cron_delivery_outcome_summary = cls(
            outcome=outcome,
            increased=increased,
            unchanged=unchanged,
            baseline=baseline,
            unknown=unknown,
        )

        obs_runtime_http_cron_delivery_outcome_summary.additional_properties = d
        return obs_runtime_http_cron_delivery_outcome_summary

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
