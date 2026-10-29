from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_budget_stop_target_last_error import (
    FinancialBudgetStopTargetLastError,
    check_financial_budget_stop_target_last_error,
)
from ..models.financial_budget_stop_target_state import (
    FinancialBudgetStopTargetState,
    check_financial_budget_stop_target_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="FinancialBudgetStopTarget")


@_attrs_define
class FinancialBudgetStopTarget:
    """Scheduler observation for one preview target. Enforced means teardown was confirmed and no resident instance
    remains. Old observations are reported as unavailable.

    """

    app_id: UUID
    state: FinancialBudgetStopTargetState
    observed_at: datetime.datetime
    resident_count: int
    environment_id: UUID | Unset = UNSET
    last_error: FinancialBudgetStopTargetLastError | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        state: str = self.state

        observed_at = self.observed_at.isoformat()

        resident_count = self.resident_count

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        last_error: str | Unset = UNSET
        if not isinstance(self.last_error, Unset):
            last_error = self.last_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "state": state,
                "observed_at": observed_at,
                "resident_count": resident_count,
            }
        )
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        state = check_financial_budget_stop_target_state(d.pop("state"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        resident_count = d.pop("resident_count")

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        _last_error = d.pop("last_error", UNSET)
        last_error: FinancialBudgetStopTargetLastError | Unset
        if isinstance(_last_error, Unset):
            last_error = UNSET
        else:
            last_error = check_financial_budget_stop_target_last_error(_last_error)

        financial_budget_stop_target = cls(
            app_id=app_id,
            state=state,
            observed_at=observed_at,
            resident_count=resident_count,
            environment_id=environment_id,
            last_error=last_error,
        )

        financial_budget_stop_target.additional_properties = d
        return financial_budget_stop_target

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
