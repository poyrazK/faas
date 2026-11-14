from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_prompt_evaluation_item_verdict import (
    OutboundAIPromptEvaluationItemVerdict,
    check_outbound_ai_prompt_evaluation_item_verdict,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIPromptEvaluationItem")


@_attrs_define
class OutboundAIPromptEvaluationItem:
    """One case's result in a managed AI prompt evaluation."""

    case: int
    verdict: OutboundAIPromptEvaluationItemVerdict
    baseline_status: int | None
    candidate_status: int | None
    baseline_cost_millicents: int
    candidate_cost_millicents: int
    baseline_ttfb_ms: int | None
    candidate_ttfb_ms: int | None
    baseline_output_text: str | Unset = UNSET
    candidate_output_text: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        case = self.case

        verdict: str = self.verdict

        baseline_status: int | None
        baseline_status = self.baseline_status

        candidate_status: int | None
        candidate_status = self.candidate_status

        baseline_cost_millicents = self.baseline_cost_millicents

        candidate_cost_millicents = self.candidate_cost_millicents

        baseline_ttfb_ms: int | None
        baseline_ttfb_ms = self.baseline_ttfb_ms

        candidate_ttfb_ms: int | None
        candidate_ttfb_ms = self.candidate_ttfb_ms

        baseline_output_text = self.baseline_output_text

        candidate_output_text = self.candidate_output_text

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "case": case,
                "verdict": verdict,
                "baseline_status": baseline_status,
                "candidate_status": candidate_status,
                "baseline_cost_millicents": baseline_cost_millicents,
                "candidate_cost_millicents": candidate_cost_millicents,
                "baseline_ttfb_ms": baseline_ttfb_ms,
                "candidate_ttfb_ms": candidate_ttfb_ms,
            }
        )
        if baseline_output_text is not UNSET:
            field_dict["baseline_output_text"] = baseline_output_text
        if candidate_output_text is not UNSET:
            field_dict["candidate_output_text"] = candidate_output_text

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        case = d.pop("case")

        verdict = check_outbound_ai_prompt_evaluation_item_verdict(d.pop("verdict"))

        def _parse_baseline_status(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        baseline_status = _parse_baseline_status(d.pop("baseline_status"))

        def _parse_candidate_status(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        candidate_status = _parse_candidate_status(d.pop("candidate_status"))

        baseline_cost_millicents = d.pop("baseline_cost_millicents")

        candidate_cost_millicents = d.pop("candidate_cost_millicents")

        def _parse_baseline_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        baseline_ttfb_ms = _parse_baseline_ttfb_ms(d.pop("baseline_ttfb_ms"))

        def _parse_candidate_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        candidate_ttfb_ms = _parse_candidate_ttfb_ms(d.pop("candidate_ttfb_ms"))

        baseline_output_text = d.pop("baseline_output_text", UNSET)

        candidate_output_text = d.pop("candidate_output_text", UNSET)

        outbound_ai_prompt_evaluation_item = cls(
            case=case,
            verdict=verdict,
            baseline_status=baseline_status,
            candidate_status=candidate_status,
            baseline_cost_millicents=baseline_cost_millicents,
            candidate_cost_millicents=candidate_cost_millicents,
            baseline_ttfb_ms=baseline_ttfb_ms,
            candidate_ttfb_ms=candidate_ttfb_ms,
            baseline_output_text=baseline_output_text,
            candidate_output_text=candidate_output_text,
        )

        outbound_ai_prompt_evaluation_item.additional_properties = d
        return outbound_ai_prompt_evaluation_item

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
