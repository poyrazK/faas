from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_evidence_explanation import DebugEvidenceExplanation
    from ..models.debug_regression_item import DebugRegressionItem
    from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem
    from ..models.debug_telemetry_span import DebugTelemetrySpan


T = TypeVar("T", bound="DebugRequestEvidenceResponse")


@_attrs_define
class DebugRequestEvidenceResponse:
    """Request metadata, bounded span evidence, matching regression, and explanation."""

    request: DebugTelemetryRequestItem
    """One bounded latency-bucket row representing gateway-served requests, persisted by the recorder/publisher."""
    spans: list[DebugTelemetrySpan]
    spans_truncated: bool
    explanation: DebugEvidenceExplanation
    """Deterministic explanation generated from the safe evidence payload."""
    generated_at: datetime.datetime
    regression: DebugRegressionItem | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.debug_regression_item import DebugRegressionItem

        request = self.request.to_dict()

        spans = []
        for spans_item_data in self.spans:
            spans_item = spans_item_data.to_dict()
            spans.append(spans_item)

        spans_truncated = self.spans_truncated

        explanation = self.explanation.to_dict()

        generated_at = self.generated_at.isoformat()

        regression: dict[str, Any] | None | Unset
        if isinstance(self.regression, Unset):
            regression = UNSET
        elif isinstance(self.regression, DebugRegressionItem):
            regression = self.regression.to_dict()
        else:
            regression = self.regression

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request": request,
                "spans": spans,
                "spans_truncated": spans_truncated,
                "explanation": explanation,
                "generated_at": generated_at,
            }
        )
        if regression is not UNSET:
            field_dict["regression"] = regression

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_evidence_explanation import DebugEvidenceExplanation
        from ..models.debug_regression_item import DebugRegressionItem
        from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem
        from ..models.debug_telemetry_span import DebugTelemetrySpan

        d = dict(src_dict)
        request = DebugTelemetryRequestItem.from_dict(d.pop("request"))

        spans = []
        _spans = d.pop("spans")
        for spans_item_data in _spans:
            spans_item = DebugTelemetrySpan.from_dict(spans_item_data)

            spans.append(spans_item)

        spans_truncated = d.pop("spans_truncated")

        explanation = DebugEvidenceExplanation.from_dict(d.pop("explanation"))

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        def _parse_regression(data: object) -> DebugRegressionItem | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                regression_type_0 = DebugRegressionItem.from_dict(data)

                return regression_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(DebugRegressionItem | None | Unset, data)

        regression = _parse_regression(d.pop("regression", UNSET))

        debug_request_evidence_response = cls(
            request=request,
            spans=spans,
            spans_truncated=spans_truncated,
            explanation=explanation,
            generated_at=generated_at,
            regression=regression,
        )

        debug_request_evidence_response.additional_properties = d
        return debug_request_evidence_response

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
