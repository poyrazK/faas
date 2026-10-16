from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_evaluation_item_verdict import (
    OutboundAIEvaluationItemVerdict,
    check_outbound_ai_evaluation_item_verdict,
)

T = TypeVar("T", bound="OutboundAIEvaluationItem")


@_attrs_define
class OutboundAIEvaluationItem:
    """One sample: a captured call of the first target and the candidate's replay of it. A judge error counts as a tie.
    Output texts are redacted and cut to 4 KiB.

    """

    log_id: UUID
    verdict: OutboundAIEvaluationItemVerdict
    candidate_status: int | None
    candidate_cost_millicents: int
    candidate_ttfb_ms: int | None
    baseline_cost_millicents: int
    baseline_ttfb_ms: int | None
    baseline_output_text: str
    candidate_output_text: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        log_id = str(self.log_id)

        verdict: str = self.verdict

        candidate_status: int | None
        candidate_status = self.candidate_status

        candidate_cost_millicents = self.candidate_cost_millicents

        candidate_ttfb_ms: int | None
        candidate_ttfb_ms = self.candidate_ttfb_ms

        baseline_cost_millicents = self.baseline_cost_millicents

        baseline_ttfb_ms: int | None
        baseline_ttfb_ms = self.baseline_ttfb_ms

        baseline_output_text = self.baseline_output_text

        candidate_output_text = self.candidate_output_text

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "log_id": log_id,
                "verdict": verdict,
                "candidate_status": candidate_status,
                "candidate_cost_millicents": candidate_cost_millicents,
                "candidate_ttfb_ms": candidate_ttfb_ms,
                "baseline_cost_millicents": baseline_cost_millicents,
                "baseline_ttfb_ms": baseline_ttfb_ms,
                "baseline_output_text": baseline_output_text,
                "candidate_output_text": candidate_output_text,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        log_id = UUID(d.pop("log_id"))

        verdict = check_outbound_ai_evaluation_item_verdict(d.pop("verdict"))

        def _parse_candidate_status(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        candidate_status = _parse_candidate_status(d.pop("candidate_status"))

        candidate_cost_millicents = d.pop("candidate_cost_millicents")

        def _parse_candidate_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        candidate_ttfb_ms = _parse_candidate_ttfb_ms(d.pop("candidate_ttfb_ms"))

        baseline_cost_millicents = d.pop("baseline_cost_millicents")

        def _parse_baseline_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        baseline_ttfb_ms = _parse_baseline_ttfb_ms(d.pop("baseline_ttfb_ms"))

        baseline_output_text = d.pop("baseline_output_text")

        candidate_output_text = d.pop("candidate_output_text")

        outbound_ai_evaluation_item = cls(
            log_id=log_id,
            verdict=verdict,
            candidate_status=candidate_status,
            candidate_cost_millicents=candidate_cost_millicents,
            candidate_ttfb_ms=candidate_ttfb_ms,
            baseline_cost_millicents=baseline_cost_millicents,
            baseline_ttfb_ms=baseline_ttfb_ms,
            baseline_output_text=baseline_output_text,
            candidate_output_text=candidate_output_text,
        )

        outbound_ai_evaluation_item.additional_properties = d
        return outbound_ai_evaluation_item

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
