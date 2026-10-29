from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_budget_stop_resume_rule import (
    FinancialBudgetStopResumeRule,
    check_financial_budget_stop_resume_rule,
)

if TYPE_CHECKING:
    from ..models.financial_budget_evaluation import FinancialBudgetEvaluation
    from ..models.financial_budget_stop_target import FinancialBudgetStopTarget


T = TypeVar("T", bound="FinancialBudgetStop")


@_attrs_define
class FinancialBudgetStop:
    """Latched monitored compute threshold decision. Corrections cannot resume work. Policy revisions release only their
    own hold; in-flight teardown may finish. New matching previews are held too. Metering, scheduler and teardown delay
    can exceed the limit.

    """

    id: UUID
    policy_id: UUID
    revision: int
    period_start: datetime.datetime
    period_end: datetime.datetime
    requested_at: datetime.datetime
    drain_deadline: datetime.datetime
    resume_rule: FinancialBudgetStopResumeRule
    evaluation: FinancialBudgetEvaluation
    """Retained evidence boundary; an absent known_millicents means evaluation unavailable, never zero. Partial
    known costs can trigger labelled notifications; stale observations cannot create new alerts."""
    targets: list[FinancialBudgetStopTarget]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        policy_id = str(self.policy_id)

        revision = self.revision

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        requested_at = self.requested_at.isoformat()

        drain_deadline = self.drain_deadline.isoformat()

        resume_rule: str = self.resume_rule

        evaluation = self.evaluation.to_dict()

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "policy_id": policy_id,
                "revision": revision,
                "period_start": period_start,
                "period_end": period_end,
                "requested_at": requested_at,
                "drain_deadline": drain_deadline,
                "resume_rule": resume_rule,
                "evaluation": evaluation,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_evaluation import FinancialBudgetEvaluation
        from ..models.financial_budget_stop_target import FinancialBudgetStopTarget

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        policy_id = UUID(d.pop("policy_id"))

        revision = d.pop("revision")

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        drain_deadline = datetime.datetime.fromisoformat(d.pop("drain_deadline"))

        resume_rule = check_financial_budget_stop_resume_rule(d.pop("resume_rule"))

        evaluation = FinancialBudgetEvaluation.from_dict(d.pop("evaluation"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = FinancialBudgetStopTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        financial_budget_stop = cls(
            id=id,
            policy_id=policy_id,
            revision=revision,
            period_start=period_start,
            period_end=period_end,
            requested_at=requested_at,
            drain_deadline=drain_deadline,
            resume_rule=resume_rule,
            evaluation=evaluation,
            targets=targets,
        )

        financial_budget_stop.additional_properties = d
        return financial_budget_stop

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
