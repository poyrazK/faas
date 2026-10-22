from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_gate_result_kind import ReleaseGateResultKind, check_release_gate_result_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="ReleaseGateResult")


@_attrs_define
class ReleaseGateResult:
    """One judged release gate in a rollout decision. Aggregates only: no routes, messages or metric labels."""

    name: str
    kind: ReleaseGateResultKind
    passed: bool
    observed: float
    """The burn rate, new error groups, metric average, failed-probe percentage, or signed change in percent."""
    bound: float
    """The value that fails the gate."""
    samples: int
    """The release's requests, new-error events, probes, or metric values."""
    summary: str
    """What the gate measured, in one line."""
    baseline_bad_fraction: float | Unset = UNSET
    """slo gates: the predecessor's bad fraction."""
    baseline: float | Unset = UNSET
    """synthetic gates: the stable revision's failed-probe percentage; metric_change gates: the metric's average
    before the rollout."""
    error_classes: list[str] | Unset = UNSET
    """new_errors gates: the new groups' error classes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        kind: str = self.kind

        passed = self.passed

        observed = self.observed

        bound = self.bound

        samples = self.samples

        summary = self.summary

        baseline_bad_fraction = self.baseline_bad_fraction

        baseline = self.baseline

        error_classes: list[str] | Unset = UNSET
        if not isinstance(self.error_classes, Unset):
            error_classes = self.error_classes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "kind": kind,
                "passed": passed,
                "observed": observed,
                "bound": bound,
                "samples": samples,
                "summary": summary,
            }
        )
        if baseline_bad_fraction is not UNSET:
            field_dict["baseline_bad_fraction"] = baseline_bad_fraction
        if baseline is not UNSET:
            field_dict["baseline"] = baseline
        if error_classes is not UNSET:
            field_dict["error_classes"] = error_classes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        kind = check_release_gate_result_kind(d.pop("kind"))

        passed = d.pop("passed")

        observed = d.pop("observed")

        bound = d.pop("bound")

        samples = d.pop("samples")

        summary = d.pop("summary")

        baseline_bad_fraction = d.pop("baseline_bad_fraction", UNSET)

        baseline = d.pop("baseline", UNSET)

        error_classes = cast(list[str], d.pop("error_classes", UNSET))

        release_gate_result = cls(
            name=name,
            kind=kind,
            passed=passed,
            observed=observed,
            bound=bound,
            samples=samples,
            summary=summary,
            baseline_bad_fraction=baseline_bad_fraction,
            baseline=baseline,
            error_classes=error_classes,
        )

        release_gate_result.additional_properties = d
        return release_gate_result

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
