from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_evidence_finding_confidence import (
    DebugEvidenceFindingConfidence,
    check_debug_evidence_finding_confidence,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugEvidenceFinding")


@_attrs_define
class DebugEvidenceFinding:
    """One bounded, evidence-backed debugger finding."""

    code: str
    title: str
    detail: str
    confidence: DebugEvidenceFindingConfidence
    evidence_refs: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        title = self.title

        detail = self.detail

        confidence: str = self.confidence

        evidence_refs: list[str] | Unset = UNSET
        if not isinstance(self.evidence_refs, Unset):
            evidence_refs = self.evidence_refs

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "title": title,
                "detail": detail,
                "confidence": confidence,
            }
        )
        if evidence_refs is not UNSET:
            field_dict["evidence_refs"] = evidence_refs

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        title = d.pop("title")

        detail = d.pop("detail")

        confidence = check_debug_evidence_finding_confidence(d.pop("confidence"))

        evidence_refs = cast(list[str], d.pop("evidence_refs", UNSET))

        debug_evidence_finding = cls(
            code=code,
            title=title,
            detail=detail,
            confidence=confidence,
            evidence_refs=evidence_refs,
        )

        debug_evidence_finding.additional_properties = d
        return debug_evidence_finding

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
