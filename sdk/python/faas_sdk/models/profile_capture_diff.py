from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_capture_diff_kind import ProfileCaptureDiffKind, check_profile_capture_diff_kind
from ..models.profile_capture_diff_unit import ProfileCaptureDiffUnit, check_profile_capture_diff_unit

if TYPE_CHECKING:
    from ..models.profile_capture import ProfileCapture
    from ..models.profile_capture_function_delta import ProfileCaptureFunctionDelta
    from ..models.profile_capture_stack_delta import ProfileCaptureStackDelta


T = TypeVar("T", bound="ProfileCaptureDiff")


@_attrs_define
class ProfileCaptureDiff:
    """One kind compared between two ready captures of an app (ADR-1155). Values keep the kind's unit and are not rescaled."""

    baseline: ProfileCapture
    """An on-demand capture. processes counts instrumented processes that saw the capture; zero means the instance
    has no collector."""
    candidate: ProfileCapture
    """An on-demand capture. processes counts instrumented processes that saw the capture; zero means the instance
    has no collector."""
    kind: ProfileCaptureDiffKind
    unit: ProfileCaptureDiffUnit
    baseline_total: int
    candidate_total: int
    delta: int
    functions: list[ProfileCaptureFunctionDelta]
    flamegraph: ProfileCaptureStackDelta
    """One call path in both captures."""
    warnings: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        kind: str = self.kind

        unit: str = self.unit

        baseline_total = self.baseline_total

        candidate_total = self.candidate_total

        delta = self.delta

        functions = []
        for functions_item_data in self.functions:
            functions_item = functions_item_data.to_dict()
            functions.append(functions_item)

        flamegraph = self.flamegraph.to_dict()

        warnings = self.warnings

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "baseline": baseline,
                "candidate": candidate,
                "kind": kind,
                "unit": unit,
                "baseline_total": baseline_total,
                "candidate_total": candidate_total,
                "delta": delta,
                "functions": functions,
                "flamegraph": flamegraph,
                "warnings": warnings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_capture import ProfileCapture
        from ..models.profile_capture_function_delta import ProfileCaptureFunctionDelta
        from ..models.profile_capture_stack_delta import ProfileCaptureStackDelta

        d = dict(src_dict)
        baseline = ProfileCapture.from_dict(d.pop("baseline"))

        candidate = ProfileCapture.from_dict(d.pop("candidate"))

        kind = check_profile_capture_diff_kind(d.pop("kind"))

        unit = check_profile_capture_diff_unit(d.pop("unit"))

        baseline_total = d.pop("baseline_total")

        candidate_total = d.pop("candidate_total")

        delta = d.pop("delta")

        functions = []
        _functions = d.pop("functions")
        for functions_item_data in _functions:
            functions_item = ProfileCaptureFunctionDelta.from_dict(functions_item_data)

            functions.append(functions_item)

        flamegraph = ProfileCaptureStackDelta.from_dict(d.pop("flamegraph"))

        warnings = cast(list[str], d.pop("warnings"))

        profile_capture_diff = cls(
            baseline=baseline,
            candidate=candidate,
            kind=kind,
            unit=unit,
            baseline_total=baseline_total,
            candidate_total=candidate_total,
            delta=delta,
            functions=functions,
            flamegraph=flamegraph,
            warnings=warnings,
        )

        profile_capture_diff.additional_properties = d
        return profile_capture_diff

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
