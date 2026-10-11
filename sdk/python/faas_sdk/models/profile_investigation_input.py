from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_call_path import ProfileCallPath
    from ..models.profile_query import ProfileQuery


T = TypeVar("T", bound="ProfileInvestigationInput")


@_attrs_define
class ProfileInvestigationInput:
    """Saved comparison metadata. Title allows 160 UTF-8 bytes and findings and notes 8192 bytes each. Selections share a
    runtime. Changed windows must belong to retained deployments; unchanged expired windows permit commentary edits.

    """

    title: str
    findings: str
    notes: str
    baseline: ProfileQuery
    """Authorized deployment CPU capture window."""
    candidate: ProfileQuery
    """Authorized deployment CPU capture window."""
    selected_path: ProfileCallPath | Unset = UNSET
    """Root-to-frame selection. Named comparison frames ignore sampled line changes; candidate and anonymous frames
    match their sampled line. Total name and file text is limited to 16384 UTF-8 bytes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        title = self.title

        findings = self.findings

        notes = self.notes

        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        selected_path: dict[str, Any] | Unset = UNSET
        if not isinstance(self.selected_path, Unset):
            selected_path = self.selected_path.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "title": title,
                "findings": findings,
                "notes": notes,
                "baseline": baseline,
                "candidate": candidate,
            }
        )
        if selected_path is not UNSET:
            field_dict["selected_path"] = selected_path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_call_path import ProfileCallPath
        from ..models.profile_query import ProfileQuery

        d = dict(src_dict)
        title = d.pop("title")

        findings = d.pop("findings")

        notes = d.pop("notes")

        baseline = ProfileQuery.from_dict(d.pop("baseline"))

        candidate = ProfileQuery.from_dict(d.pop("candidate"))

        _selected_path = d.pop("selected_path", UNSET)
        selected_path: ProfileCallPath | Unset
        if isinstance(_selected_path, Unset):
            selected_path = UNSET
        else:
            selected_path = ProfileCallPath.from_dict(_selected_path)

        profile_investigation_input = cls(
            title=title,
            findings=findings,
            notes=notes,
            baseline=baseline,
            candidate=candidate,
            selected_path=selected_path,
        )

        profile_investigation_input.additional_properties = d
        return profile_investigation_input

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
