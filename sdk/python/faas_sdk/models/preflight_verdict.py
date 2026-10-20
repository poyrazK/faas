from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.preflight_level import PreflightLevel, check_preflight_level
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.preflight_finding import PreflightFinding
    from ..models.preflight_profile import PreflightProfile


T = TypeVar("T", bound="PreflightVerdict")


@_attrs_define
class PreflightVerdict:
    """The assessed result for one source tree: a headline level, the findings
    behind it, and the run contract that was inferred.

    """

    level: PreflightLevel
    """`green` satisfies the container contract as written, `amber` needs a
    declared change, `red` cannot run.
    """
    profile: PreflightProfile
    """The run contract inferred from the source tree."""
    findings: list[PreflightFinding] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        level: str = self.level

        profile = self.profile.to_dict()

        findings: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.findings, Unset):
            findings = []
            for findings_item_data in self.findings:
                findings_item = findings_item_data.to_dict()
                findings.append(findings_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "level": level,
                "profile": profile,
            }
        )
        if findings is not UNSET:
            field_dict["findings"] = findings

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.preflight_finding import PreflightFinding
        from ..models.preflight_profile import PreflightProfile

        d = dict(src_dict)
        level = check_preflight_level(d.pop("level"))

        profile = PreflightProfile.from_dict(d.pop("profile"))

        _findings = d.pop("findings", UNSET)
        findings: list[PreflightFinding] | Unset = UNSET
        if _findings is not UNSET:
            findings = []
            for findings_item_data in _findings:
                findings_item = PreflightFinding.from_dict(findings_item_data)

                findings.append(findings_item)

        preflight_verdict = cls(
            level=level,
            profile=profile,
            findings=findings,
        )

        preflight_verdict.additional_properties = d
        return preflight_verdict

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
