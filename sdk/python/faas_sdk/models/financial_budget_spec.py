from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.financial_budget_spec_action import FinancialBudgetSpecAction, check_financial_budget_spec_action
from ..models.financial_budget_spec_basis import FinancialBudgetSpecBasis, check_financial_budget_spec_basis
from ..models.financial_budget_spec_currency import FinancialBudgetSpecCurrency, check_financial_budget_spec_currency
from ..models.financial_budget_spec_meters_item import (
    FinancialBudgetSpecMetersItem,
    check_financial_budget_spec_meters_item,
)
from ..models.financial_budget_spec_mode import FinancialBudgetSpecMode, check_financial_budget_spec_mode
from ..models.financial_budget_spec_resume_rule import (
    FinancialBudgetSpecResumeRule,
    check_financial_budget_spec_resume_rule,
)

if TYPE_CHECKING:
    from ..models.financial_budget_scope import FinancialBudgetScope


T = TypeVar("T", bound="FinancialBudgetSpec")


@_attrs_define
class FinancialBudgetSpec:
    """Customer budget intent; activation and enforcement are separately acknowledged."""

    name: str
    """Nonblank name bounded to 128 UTF-8 bytes."""
    scope: FinancialBudgetScope
    """Authoritative account or resource identity; resource ids must belong to the account."""
    currency: FinancialBudgetSpecCurrency
    meters: list[FinancialBudgetSpecMetersItem]
    """Sorted meter names; strict mode covers compute only."""
    basis: FinancialBudgetSpecBasis
    """Net usage after the shared account allowance or gross usage before it; strict resource scopes require gross
    usage."""
    limit_millicents: int
    notify_millicents: list[int]
    """Increasing nonnegative thresholds at or below the limit."""
    mode: FinancialBudgetSpecMode
    action: FinancialBudgetSpecAction
    drain_seconds: int
    """Notify uses zero; stopping targets drain before the deadline."""
    resume_rule: FinancialBudgetSpecResumeRule
    enabled: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        scope = self.scope.to_dict()

        currency: str = self.currency

        meters = []
        for meters_item_data in self.meters:
            meters_item: str = meters_item_data
            meters.append(meters_item)

        basis: str = self.basis

        limit_millicents = self.limit_millicents

        notify_millicents = self.notify_millicents

        mode: str = self.mode

        action: str = self.action

        drain_seconds = self.drain_seconds

        resume_rule: str = self.resume_rule

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "scope": scope,
                "currency": currency,
                "meters": meters,
                "basis": basis,
                "limit_millicents": limit_millicents,
                "notify_millicents": notify_millicents,
                "mode": mode,
                "action": action,
                "drain_seconds": drain_seconds,
                "resume_rule": resume_rule,
                "enabled": enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_scope import FinancialBudgetScope

        d = dict(src_dict)
        name = d.pop("name")

        scope = FinancialBudgetScope.from_dict(d.pop("scope"))

        currency = check_financial_budget_spec_currency(d.pop("currency"))

        meters = []
        _meters = d.pop("meters")
        for meters_item_data in _meters:
            meters_item = check_financial_budget_spec_meters_item(meters_item_data)

            meters.append(meters_item)

        basis = check_financial_budget_spec_basis(d.pop("basis"))

        limit_millicents = d.pop("limit_millicents")

        notify_millicents = cast(list[int], d.pop("notify_millicents"))

        mode = check_financial_budget_spec_mode(d.pop("mode"))

        action = check_financial_budget_spec_action(d.pop("action"))

        drain_seconds = d.pop("drain_seconds")

        resume_rule = check_financial_budget_spec_resume_rule(d.pop("resume_rule"))

        enabled = d.pop("enabled")

        financial_budget_spec = cls(
            name=name,
            scope=scope,
            currency=currency,
            meters=meters,
            basis=basis,
            limit_millicents=limit_millicents,
            notify_millicents=notify_millicents,
            mode=mode,
            action=action,
            drain_seconds=drain_seconds,
            resume_rule=resume_rule,
            enabled=enabled,
        )

        financial_budget_spec.additional_properties = d
        return financial_budget_spec

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
