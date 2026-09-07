from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_evidence_explanation_status import (
    DebugEvidenceExplanationStatus,
    check_debug_evidence_explanation_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_telemetry_span import DebugTelemetrySpan


T = TypeVar("T", bound="DebugEvidenceExplanation")


@_attrs_define
class DebugEvidenceExplanation:
    """Deterministic explanation generated from the safe evidence payload."""

    status: DebugEvidenceExplanationStatus
    headline: str
    primary_span: DebugTelemetrySpan | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.debug_telemetry_span import DebugTelemetrySpan

        status: str = self.status

        headline = self.headline

        primary_span: dict[str, Any] | None | Unset
        if isinstance(self.primary_span, Unset):
            primary_span = UNSET
        elif isinstance(self.primary_span, DebugTelemetrySpan):
            primary_span = self.primary_span.to_dict()
        else:
            primary_span = self.primary_span

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "headline": headline,
            }
        )
        if primary_span is not UNSET:
            field_dict["primary_span"] = primary_span

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_telemetry_span import DebugTelemetrySpan

        d = dict(src_dict)
        status = check_debug_evidence_explanation_status(d.pop("status"))

        headline = d.pop("headline")

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

        debug_evidence_explanation = cls(
            status=status,
            headline=headline,
            primary_span=primary_span,
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
