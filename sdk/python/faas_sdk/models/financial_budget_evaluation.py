from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="FinancialBudgetEvaluation")


@_attrs_define
class FinancialBudgetEvaluation:
    """Retained evidence boundary; an absent known_millicents means evaluation unavailable, never zero. Partial known costs
    can trigger labelled notifications; stale observations cannot create new alerts.

    """

    period_start: datetime.datetime
    period_end: datetime.datetime
    as_of: datetime.datetime
    evaluated_at: datetime.datetime
    evidence_through_id: int
    coverage_complete: bool
    fresh: bool
    reasons: list[str]
    known_millicents: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        as_of = self.as_of.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        evidence_through_id = self.evidence_through_id

        coverage_complete = self.coverage_complete

        fresh = self.fresh

        reasons = self.reasons

        known_millicents = self.known_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "period_start": period_start,
                "period_end": period_end,
                "as_of": as_of,
                "evaluated_at": evaluated_at,
                "evidence_through_id": evidence_through_id,
                "coverage_complete": coverage_complete,
                "fresh": fresh,
                "reasons": reasons,
            }
        )
        if known_millicents is not UNSET:
            field_dict["known_millicents"] = known_millicents

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        evidence_through_id = d.pop("evidence_through_id")

        coverage_complete = d.pop("coverage_complete")

        fresh = d.pop("fresh")

        reasons = cast(list[str], d.pop("reasons"))

        known_millicents = d.pop("known_millicents", UNSET)

        financial_budget_evaluation = cls(
            period_start=period_start,
            period_end=period_end,
            as_of=as_of,
            evaluated_at=evaluated_at,
            evidence_through_id=evidence_through_id,
            coverage_complete=coverage_complete,
            fresh=fresh,
            reasons=reasons,
            known_millicents=known_millicents,
        )

        financial_budget_evaluation.additional_properties = d
        return financial_budget_evaluation

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
