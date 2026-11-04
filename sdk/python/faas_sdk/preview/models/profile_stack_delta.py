from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_source_location import ProfileSourceLocation


T = TypeVar("T", bound="ProfileStackDelta")


@_attrs_define
class ProfileStackDelta:
    """Inclusive CPU rates for one complete caller path. Rates are normalized by each selected window; omitted rates mean
    the path was not observed, not measured zero. Delta is omitted unless both sides were observed. Width is the sum of
    observed rates for an additive union layout. Named frames match by symbol and file within their parent path; unknown
    and anonymous frames also retain their line identity.

    """

    name: str
    width_cpu_per_second: float
    file: str | Unset = UNSET
    baseline_line: int | Unset = UNSET
    candidate_line: int | Unset = UNSET
    baseline_source: ProfileSourceLocation | Unset = UNSET
    """Safely mapped sampled source line at the deployment's recorded commit. Omitted for missing symbols, unknown
    paths, generated adapters, or exhausted link bounds. Private repository access is enforced by GitHub."""
    candidate_source: ProfileSourceLocation | Unset = UNSET
    """Safely mapped sampled source line at the deployment's recorded commit. Omitted for missing symbols, unknown
    paths, generated adapters, or exhausted link bounds. Private repository access is enforced by GitHub."""
    baseline_cpu_per_second: float | Unset = UNSET
    candidate_cpu_per_second: float | Unset = UNSET
    delta_cpu_per_second: float | Unset = UNSET
    children: list[ProfileStackDelta] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        width_cpu_per_second = self.width_cpu_per_second

        file = self.file

        baseline_line = self.baseline_line

        candidate_line = self.candidate_line

        baseline_source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline_source, Unset):
            baseline_source = self.baseline_source.to_dict()

        candidate_source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate_source, Unset):
            candidate_source = self.candidate_source.to_dict()

        baseline_cpu_per_second = self.baseline_cpu_per_second

        candidate_cpu_per_second = self.candidate_cpu_per_second

        delta_cpu_per_second = self.delta_cpu_per_second

        children: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.children, Unset):
            children = []
            for children_item_data in self.children:
                children_item = children_item_data.to_dict()
                children.append(children_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "width_cpu_per_second": width_cpu_per_second,
            }
        )
        if file is not UNSET:
            field_dict["file"] = file
        if baseline_line is not UNSET:
            field_dict["baseline_line"] = baseline_line
        if candidate_line is not UNSET:
            field_dict["candidate_line"] = candidate_line
        if baseline_source is not UNSET:
            field_dict["baseline_source"] = baseline_source
        if candidate_source is not UNSET:
            field_dict["candidate_source"] = candidate_source
        if baseline_cpu_per_second is not UNSET:
            field_dict["baseline_cpu_per_second"] = baseline_cpu_per_second
        if candidate_cpu_per_second is not UNSET:
            field_dict["candidate_cpu_per_second"] = candidate_cpu_per_second
        if delta_cpu_per_second is not UNSET:
            field_dict["delta_cpu_per_second"] = delta_cpu_per_second
        if children is not UNSET:
            field_dict["children"] = children

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_source_location import ProfileSourceLocation

        d = dict(src_dict)
        name = d.pop("name")

        width_cpu_per_second = d.pop("width_cpu_per_second")

        file = d.pop("file", UNSET)

        baseline_line = d.pop("baseline_line", UNSET)

        candidate_line = d.pop("candidate_line", UNSET)

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

        baseline_cpu_per_second = d.pop("baseline_cpu_per_second", UNSET)

        candidate_cpu_per_second = d.pop("candidate_cpu_per_second", UNSET)

        delta_cpu_per_second = d.pop("delta_cpu_per_second", UNSET)

        _children = d.pop("children", UNSET)
        children: list[ProfileStackDelta] | Unset = UNSET
        if _children is not UNSET:
            children = []
            for children_item_data in _children:
                children_item = ProfileStackDelta.from_dict(children_item_data)

                children.append(children_item)

        profile_stack_delta = cls(
            name=name,
            width_cpu_per_second=width_cpu_per_second,
            file=file,
            baseline_line=baseline_line,
            candidate_line=candidate_line,
            baseline_source=baseline_source,
            candidate_source=candidate_source,
            baseline_cpu_per_second=baseline_cpu_per_second,
            candidate_cpu_per_second=candidate_cpu_per_second,
            delta_cpu_per_second=delta_cpu_per_second,
            children=children,
        )

        profile_stack_delta.additional_properties = d
        return profile_stack_delta

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
