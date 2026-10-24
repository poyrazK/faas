from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_release_gate_request_comparison import (
    CreateReleaseGateRequestComparison,
    check_create_release_gate_request_comparison,
)
from ..models.create_release_gate_request_kind import (
    CreateReleaseGateRequestKind,
    check_create_release_gate_request_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateReleaseGateRequest")


@_attrs_define
class CreateReleaseGateRequest:
    """Adds a release gate. Give exactly one kind's fields; omitted numbers take Gregale's defaults."""

    name: str
    kind: CreateReleaseGateRequestKind
    slo: str | Unset = UNSET
    """slo gates: the name of one of the app's SLOs."""
    max_burn_rate: int | Unset = UNSET
    """slo gates: default 10."""
    max_new_groups: int | Unset = UNSET
    """new_errors gates: default 0."""
    min_events: int | Unset = UNSET
    """new_errors gates: default 3."""
    metric: str | Unset = UNSET
    """custom_metric gates: the custom metric's name."""
    comparison: CreateReleaseGateRequestComparison | Unset = UNSET
    """custom_metric gates: fail when the average is below or above the threshold."""
    threshold: float | Unset = UNSET
    """custom_metric gates: the bound."""
    min_samples: int | Unset = UNSET
    """Default 20."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        kind: str = self.kind

        slo = self.slo

        max_burn_rate = self.max_burn_rate

        max_new_groups = self.max_new_groups

        min_events = self.min_events

        metric = self.metric

        comparison: str | Unset = UNSET
        if not isinstance(self.comparison, Unset):
            comparison = self.comparison

        threshold = self.threshold

        min_samples = self.min_samples

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "kind": kind,
            }
        )
        if slo is not UNSET:
            field_dict["slo"] = slo
        if max_burn_rate is not UNSET:
            field_dict["max_burn_rate"] = max_burn_rate
        if max_new_groups is not UNSET:
            field_dict["max_new_groups"] = max_new_groups
        if min_events is not UNSET:
            field_dict["min_events"] = min_events
        if metric is not UNSET:
            field_dict["metric"] = metric
        if comparison is not UNSET:
            field_dict["comparison"] = comparison
        if threshold is not UNSET:
            field_dict["threshold"] = threshold
        if min_samples is not UNSET:
            field_dict["min_samples"] = min_samples

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        kind = check_create_release_gate_request_kind(d.pop("kind"))

        slo = d.pop("slo", UNSET)

        max_burn_rate = d.pop("max_burn_rate", UNSET)

        max_new_groups = d.pop("max_new_groups", UNSET)

        min_events = d.pop("min_events", UNSET)

        metric = d.pop("metric", UNSET)

        _comparison = d.pop("comparison", UNSET)
        comparison: CreateReleaseGateRequestComparison | Unset
        if isinstance(_comparison, Unset):
            comparison = UNSET
        else:
            comparison = check_create_release_gate_request_comparison(_comparison)

        threshold = d.pop("threshold", UNSET)

        min_samples = d.pop("min_samples", UNSET)

        create_release_gate_request = cls(
            name=name,
            kind=kind,
            slo=slo,
            max_burn_rate=max_burn_rate,
            max_new_groups=max_new_groups,
            min_events=min_events,
            metric=metric,
            comparison=comparison,
            threshold=threshold,
            min_samples=min_samples,
        )

        create_release_gate_request.additional_properties = d
        return create_release_gate_request

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
