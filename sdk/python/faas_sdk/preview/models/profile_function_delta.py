from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_source_location import ProfileSourceLocation


T = TypeVar("T", bound="ProfileFunctionDelta")


@_attrs_define
class ProfileFunctionDelta:
    """Function self CPU rates normalized by each selected capture window. Numeric rates are meaningful only when the
    corresponding observed flag is true; delta_cpu_per_second is meaningful only when delta_known is true. Unobserved
    functions are not measured zero.

    """

    name: str
    baseline_cpu_per_second: float
    candidate_cpu_per_second: float
    delta_cpu_per_second: float
    baseline_observed: bool
    candidate_observed: bool
    delta_known: bool
    file: str | Unset = UNSET
    line: int | Unset = UNSET
    baseline_source: ProfileSourceLocation | Unset = UNSET
    """Safely mapped sampled source line at the deployment's recorded commit. Omitted for missing symbols, unknown
    paths, generated adapters, or exhausted link bounds. Private repository access is enforced by GitHub."""
    candidate_source: ProfileSourceLocation | Unset = UNSET
    """Safely mapped sampled source line at the deployment's recorded commit. Omitted for missing symbols, unknown
    paths, generated adapters, or exhausted link bounds. Private repository access is enforced by GitHub."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        baseline_cpu_per_second = self.baseline_cpu_per_second

        candidate_cpu_per_second = self.candidate_cpu_per_second

        delta_cpu_per_second = self.delta_cpu_per_second

        baseline_observed = self.baseline_observed

        candidate_observed = self.candidate_observed

        delta_known = self.delta_known

        file = self.file

        line = self.line

        baseline_source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline_source, Unset):
            baseline_source = self.baseline_source.to_dict()

        candidate_source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate_source, Unset):
            candidate_source = self.candidate_source.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "baseline_cpu_per_second": baseline_cpu_per_second,
                "candidate_cpu_per_second": candidate_cpu_per_second,
                "delta_cpu_per_second": delta_cpu_per_second,
                "baseline_observed": baseline_observed,
                "candidate_observed": candidate_observed,
                "delta_known": delta_known,
            }
        )
        if file is not UNSET:
            field_dict["file"] = file
        if line is not UNSET:
            field_dict["line"] = line
        if baseline_source is not UNSET:
            field_dict["baseline_source"] = baseline_source
        if candidate_source is not UNSET:
            field_dict["candidate_source"] = candidate_source

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_source_location import ProfileSourceLocation

        d = dict(src_dict)
        name = d.pop("name")

        baseline_cpu_per_second = d.pop("baseline_cpu_per_second")

        candidate_cpu_per_second = d.pop("candidate_cpu_per_second")

        delta_cpu_per_second = d.pop("delta_cpu_per_second")

        baseline_observed = d.pop("baseline_observed")

        candidate_observed = d.pop("candidate_observed")

        delta_known = d.pop("delta_known")

        file = d.pop("file", UNSET)

        line = d.pop("line", UNSET)

        _baseline_source = d.pop("baseline_source", UNSET)
        baseline_source: ProfileSourceLocation | Unset
        if isinstance(_baseline_source, Unset):
            baseline_source = UNSET
        else:
            baseline_source = ProfileSourceLocation.from_dict(_baseline_source)

        _candidate_source = d.pop("candidate_source", UNSET)
        candidate_source: ProfileSourceLocation | Unset
        if isinstance(_candidate_source, Unset):
            candidate_source = UNSET
        else:
            candidate_source = ProfileSourceLocation.from_dict(_candidate_source)

        profile_function_delta = cls(
            name=name,
            baseline_cpu_per_second=baseline_cpu_per_second,
            candidate_cpu_per_second=candidate_cpu_per_second,
            delta_cpu_per_second=delta_cpu_per_second,
            baseline_observed=baseline_observed,
            candidate_observed=candidate_observed,
            delta_known=delta_known,
            file=file,
            line=line,
            baseline_source=baseline_source,
            candidate_source=candidate_source,
        )

        profile_function_delta.additional_properties = d
        return profile_function_delta

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
