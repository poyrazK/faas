from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_evidence_explanation_confidence import (
    DebugEvidenceExplanationConfidence,
    check_debug_evidence_explanation_confidence,
)
from ..models.debug_evidence_explanation_diagnosis import (
    DebugEvidenceExplanationDiagnosis,
    check_debug_evidence_explanation_diagnosis,
)
from ..models.debug_evidence_explanation_generated_by import (
    DebugEvidenceExplanationGeneratedBy,
    check_debug_evidence_explanation_generated_by,
)
from ..models.debug_evidence_explanation_status import (
    DebugEvidenceExplanationStatus,
    check_debug_evidence_explanation_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_evidence_finding import DebugEvidenceFinding
    from ..models.debug_evidence_recommendation import DebugEvidenceRecommendation
    from ..models.debug_evidence_ref import DebugEvidenceRef
    from ..models.debug_telemetry_span import DebugTelemetrySpan


T = TypeVar("T", bound="DebugEvidenceExplanation")


@_attrs_define
class DebugEvidenceExplanation:
    """Bounded root-cause synthesis generated from the safe evidence payload."""

    status: DebugEvidenceExplanationStatus
    headline: str
    diagnosis: DebugEvidenceExplanationDiagnosis | Unset = UNSET
    confidence: DebugEvidenceExplanationConfidence | Unset = UNSET
    primary_span: DebugTelemetrySpan | None | Unset = UNSET
    findings: list[DebugEvidenceFinding] | Unset = UNSET
    recommendations: list[DebugEvidenceRecommendation] | Unset = UNSET
    evidence_refs: list[DebugEvidenceRef] | Unset = UNSET
    generated_by: DebugEvidenceExplanationGeneratedBy | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.debug_telemetry_span import DebugTelemetrySpan

        status: str = self.status

        headline = self.headline

        diagnosis: str | Unset = UNSET
        if not isinstance(self.diagnosis, Unset):
            diagnosis = self.diagnosis

        confidence: str | Unset = UNSET
        if not isinstance(self.confidence, Unset):
            confidence = self.confidence

        primary_span: dict[str, Any] | None | Unset
        if isinstance(self.primary_span, Unset):
            primary_span = UNSET
        elif isinstance(self.primary_span, DebugTelemetrySpan):
            primary_span = self.primary_span.to_dict()
        else:
            primary_span = self.primary_span

        findings: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.findings, Unset):
            findings = []
            for findings_item_data in self.findings:
                findings_item = findings_item_data.to_dict()
                findings.append(findings_item)

        recommendations: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.recommendations, Unset):
            recommendations = []
            for recommendations_item_data in self.recommendations:
                recommendations_item = recommendations_item_data.to_dict()
                recommendations.append(recommendations_item)

        evidence_refs: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.evidence_refs, Unset):
            evidence_refs = []
            for evidence_refs_item_data in self.evidence_refs:
                evidence_refs_item = evidence_refs_item_data.to_dict()
                evidence_refs.append(evidence_refs_item)

        generated_by: str | Unset = UNSET
        if not isinstance(self.generated_by, Unset):
            generated_by = self.generated_by

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "headline": headline,
            }
        )
        if diagnosis is not UNSET:
            field_dict["diagnosis"] = diagnosis
        if confidence is not UNSET:
            field_dict["confidence"] = confidence
        if primary_span is not UNSET:
            field_dict["primary_span"] = primary_span
        if findings is not UNSET:
            field_dict["findings"] = findings
        if recommendations is not UNSET:
            field_dict["recommendations"] = recommendations
        if evidence_refs is not UNSET:
            field_dict["evidence_refs"] = evidence_refs
        if generated_by is not UNSET:
            field_dict["generated_by"] = generated_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_evidence_finding import DebugEvidenceFinding
        from ..models.debug_evidence_recommendation import DebugEvidenceRecommendation
        from ..models.debug_evidence_ref import DebugEvidenceRef
        from ..models.debug_telemetry_span import DebugTelemetrySpan

        d = dict(src_dict)
        status = check_debug_evidence_explanation_status(d.pop("status"))

        headline = d.pop("headline")

        _diagnosis = d.pop("diagnosis", UNSET)
        diagnosis: DebugEvidenceExplanationDiagnosis | Unset
        if isinstance(_diagnosis, Unset):
            diagnosis = UNSET
        else:
            diagnosis = check_debug_evidence_explanation_diagnosis(_diagnosis)

        _confidence = d.pop("confidence", UNSET)
        confidence: DebugEvidenceExplanationConfidence | Unset
        if isinstance(_confidence, Unset):
            confidence = UNSET
        else:
            confidence = check_debug_evidence_explanation_confidence(_confidence)

        def _parse_primary_span(data: object) -> DebugTelemetrySpan | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                primary_span_type_0 = DebugTelemetrySpan.from_dict(data)

                return primary_span_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(DebugTelemetrySpan | None | Unset, data)

        primary_span = _parse_primary_span(d.pop("primary_span", UNSET))

        _findings = d.pop("findings", UNSET)
        findings: list[DebugEvidenceFinding] | Unset = UNSET
        if _findings is not UNSET:
            findings = []
            for findings_item_data in _findings:
                findings_item = DebugEvidenceFinding.from_dict(findings_item_data)

                findings.append(findings_item)

        _recommendations = d.pop("recommendations", UNSET)
        recommendations: list[DebugEvidenceRecommendation] | Unset = UNSET
        if _recommendations is not UNSET:
            recommendations = []
            for recommendations_item_data in _recommendations:
                recommendations_item = DebugEvidenceRecommendation.from_dict(recommendations_item_data)

                recommendations.append(recommendations_item)

        _evidence_refs = d.pop("evidence_refs", UNSET)
        evidence_refs: list[DebugEvidenceRef] | Unset = UNSET
        if _evidence_refs is not UNSET:
            evidence_refs = []
            for evidence_refs_item_data in _evidence_refs:
                evidence_refs_item = DebugEvidenceRef.from_dict(evidence_refs_item_data)

                evidence_refs.append(evidence_refs_item)

        _generated_by = d.pop("generated_by", UNSET)
        generated_by: DebugEvidenceExplanationGeneratedBy | Unset
        if isinstance(_generated_by, Unset):
            generated_by = UNSET
        else:
            generated_by = check_debug_evidence_explanation_generated_by(_generated_by)

        debug_evidence_explanation = cls(
            status=status,
            headline=headline,
            diagnosis=diagnosis,
            confidence=confidence,
            primary_span=primary_span,
            findings=findings,
            recommendations=recommendations,
            evidence_refs=evidence_refs,
            generated_by=generated_by,
        )

        debug_evidence_explanation.additional_properties = d
        return debug_evidence_explanation

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
