from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_budget_alert_kind import FinancialBudgetAlertKind, check_financial_budget_alert_kind
from ..models.financial_budget_alert_last_error import (
    FinancialBudgetAlertLastError,
    check_financial_budget_alert_last_error,
)
from ..models.financial_budget_alert_state import FinancialBudgetAlertState, check_financial_budget_alert_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.financial_budget_evaluation import FinancialBudgetEvaluation


T = TypeVar("T", bound="FinancialBudgetAlert")


@_attrs_define
class FinancialBudgetAlert:
    """One threshold event per policy revision and UTC month. Edits re-evaluate monthly spending. Delivery is at least once
    with a stable transport idempotency key; delivered means accepted by the configured transport, not confirmed inbox
    receipt.

    """

    id: UUID
    policy_revision: int
    threshold_millicents: int
    evaluation: FinancialBudgetEvaluation
    """Retained evidence boundary; an absent known_millicents means evaluation unavailable, never zero. Partial
    known costs can trigger labelled notifications; stale observations cannot create new alerts."""
    state: FinancialBudgetAlertState
    attempts: int
    next_attempt_at: datetime.datetime
    kind: FinancialBudgetAlertKind | Unset = UNSET
    """Absent for a crossed threshold. projection marks a projected-overrun alert, whose threshold_millicents is
    the limit."""
    delivered_at: datetime.datetime | Unset = UNSET
    last_error: FinancialBudgetAlertLastError | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        policy_revision = self.policy_revision

        threshold_millicents = self.threshold_millicents

        evaluation = self.evaluation.to_dict()

        state: str = self.state

        attempts = self.attempts

        next_attempt_at = self.next_attempt_at.isoformat()

        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        delivered_at: str | Unset = UNSET
        if not isinstance(self.delivered_at, Unset):
            delivered_at = self.delivered_at.isoformat()

        last_error: str | Unset = UNSET
        if not isinstance(self.last_error, Unset):
            last_error = self.last_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "policy_revision": policy_revision,
                "threshold_millicents": threshold_millicents,
                "evaluation": evaluation,
                "state": state,
                "attempts": attempts,
                "next_attempt_at": next_attempt_at,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind
        if delivered_at is not UNSET:
            field_dict["delivered_at"] = delivered_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_evaluation import FinancialBudgetEvaluation

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        policy_revision = d.pop("policy_revision")

        threshold_millicents = d.pop("threshold_millicents")

        evaluation = FinancialBudgetEvaluation.from_dict(d.pop("evaluation"))

        state = check_financial_budget_alert_state(d.pop("state"))

        attempts = d.pop("attempts")

        next_attempt_at = datetime.datetime.fromisoformat(d.pop("next_attempt_at"))

        _kind = d.pop("kind", UNSET)
        kind: FinancialBudgetAlertKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_financial_budget_alert_kind(_kind)

        _delivered_at = d.pop("delivered_at", UNSET)
        delivered_at: datetime.datetime | Unset
        if isinstance(_delivered_at, Unset):
            delivered_at = UNSET
        else:
            delivered_at = datetime.datetime.fromisoformat(_delivered_at)

        _last_error = d.pop("last_error", UNSET)
        last_error: FinancialBudgetAlertLastError | Unset
        if isinstance(_last_error, Unset):
            last_error = UNSET
        else:
            last_error = check_financial_budget_alert_last_error(_last_error)

        financial_budget_alert = cls(
            id=id,
            policy_revision=policy_revision,
            threshold_millicents=threshold_millicents,
            evaluation=evaluation,
            state=state,
            attempts=attempts,
            next_attempt_at=next_attempt_at,
            kind=kind,
            delivered_at=delivered_at,
            last_error=last_error,
        )

        financial_budget_alert.additional_properties = d
        return financial_budget_alert

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
