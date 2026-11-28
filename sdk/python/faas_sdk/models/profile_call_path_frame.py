from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_source_location import ProfileSourceLocation


T = TypeVar("T", bound="ProfileCallPathFrame")


@_attrs_define
class ProfileCallPathFrame:
    """One sampled frame in a complete saved caller path. Automatic canary evidence retains only safe repository-relative
    paths and line numbers; source URLs are derived at read time from each deployment's recorded commit.

    """

    name: str
    file: str | Unset = UNSET
    line: int | Unset = UNSET
    baseline_path: str | Unset = UNSET
    """Safe repository-relative baseline path retained for automatic canary evidence."""
    baseline_line: int | Unset = UNSET
    candidate_path: str | Unset = UNSET
    """Safe repository-relative candidate path retained for automatic canary evidence."""
    candidate_line: int | Unset = UNSET
    baseline_source: ProfileSourceLocation | Unset = UNSET
    """Safely mapped sampled source line at the deployment's recorded commit. Omitted for missing symbols, unknown
    paths, generated adapters, or exhausted link bounds. Private repository access is enforced by GitHub."""
    candidate_source: ProfileSourceLocation | Unset = UNSET
    """Safely mapped sampled source line at the deployment's recorded commit. Omitted for missing symbols, unknown
    paths, generated adapters, or exhausted link bounds. Private repository access is enforced by GitHub."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        file = self.file

        line = self.line

        baseline_path = self.baseline_path

        baseline_line = self.baseline_line

        candidate_path = self.candidate_path

        candidate_line = self.candidate_line

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
            }
        )
        if file is not UNSET:
            field_dict["file"] = file
        if line is not UNSET:
            field_dict["line"] = line
        if baseline_path is not UNSET:
            field_dict["baseline_path"] = baseline_path
        if baseline_line is not UNSET:
            field_dict["baseline_line"] = baseline_line
        if candidate_path is not UNSET:
            field_dict["candidate_path"] = candidate_path
        if candidate_line is not UNSET:
            field_dict["candidate_line"] = candidate_line
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

        file = d.pop("file", UNSET)

        line = d.pop("line", UNSET)

        baseline_path = d.pop("baseline_path", UNSET)

        baseline_line = d.pop("baseline_line", UNSET)

        candidate_path = d.pop("candidate_path", UNSET)

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

        profile_call_path_frame = cls(
            name=name,
            file=file,
            line=line,
            baseline_path=baseline_path,
            baseline_line=baseline_line,
            candidate_path=candidate_path,
            candidate_line=candidate_line,
            baseline_source=baseline_source,
            candidate_source=candidate_source,
        )

        profile_call_path_frame.additional_properties = d
        return profile_call_path_frame

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
