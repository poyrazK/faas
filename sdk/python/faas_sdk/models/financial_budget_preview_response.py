from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.financial_budget_spec import FinancialBudgetSpec
    from ..models.financial_budget_target import FinancialBudgetTarget


T = TypeVar("T", bound="FinancialBudgetPreviewResponse")


@_attrs_define
class FinancialBudgetPreviewResponse:
    """Known spending, evidence gaps, workload consequences and explicit readiness."""

    spec: FinancialBudgetSpec
    """Customer budget intent; activation and enforcement are separately acknowledged."""
    period_start: datetime.datetime
    period_end: datetime.datetime
    as_of: datetime.datetime
    known_millicents: int
    known_limit_reached: bool
    """Known subtotal is at or above the limit; inspect coverage before inferring complete spending."""
    coverage_complete: bool
    fresh: bool
    reasons: list[str]
    enforcement_ready: bool
    """False while durable decisions and owner integrations lack acceptance; preview never activates a policy."""
    guarantee: str
    targets: list[FinancialBudgetTarget]
    continuing_targets: list[FinancialBudgetTarget]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        spec = self.spec.to_dict()

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        as_of = self.as_of.isoformat()

        known_millicents = self.known_millicents

        known_limit_reached = self.known_limit_reached

        coverage_complete = self.coverage_complete

        fresh = self.fresh

        reasons = self.reasons

        enforcement_ready = self.enforcement_ready

        guarantee = self.guarantee

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        continuing_targets = []
        for continuing_targets_item_data in self.continuing_targets:
            continuing_targets_item = continuing_targets_item_data.to_dict()
            continuing_targets.append(continuing_targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "spec": spec,
                "period_start": period_start,
                "period_end": period_end,
                "as_of": as_of,
                "known_millicents": known_millicents,
                "known_limit_reached": known_limit_reached,
                "coverage_complete": coverage_complete,
                "fresh": fresh,
                "reasons": reasons,
                "enforcement_ready": enforcement_ready,
                "guarantee": guarantee,
                "targets": targets,
                "continuing_targets": continuing_targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.financial_budget_spec import FinancialBudgetSpec
        from ..models.financial_budget_target import FinancialBudgetTarget

        d = dict(src_dict)
        spec = FinancialBudgetSpec.from_dict(d.pop("spec"))

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        known_millicents = d.pop("known_millicents")

        known_limit_reached = d.pop("known_limit_reached")

        coverage_complete = d.pop("coverage_complete")

        fresh = d.pop("fresh")

        reasons = cast(list[str], d.pop("reasons"))

        enforcement_ready = d.pop("enforcement_ready")

        guarantee = d.pop("guarantee")

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = FinancialBudgetTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        continuing_targets = []
        _continuing_targets = d.pop("continuing_targets")
        for continuing_targets_item_data in _continuing_targets:
            continuing_targets_item = FinancialBudgetTarget.from_dict(continuing_targets_item_data)

            continuing_targets.append(continuing_targets_item)

        financial_budget_preview_response = cls(
            spec=spec,
            period_start=period_start,
            period_end=period_end,
            as_of=as_of,
            known_millicents=known_millicents,
            known_limit_reached=known_limit_reached,
            coverage_complete=coverage_complete,
            fresh=fresh,
            reasons=reasons,
            enforcement_ready=enforcement_ready,
            guarantee=guarantee,
            targets=targets,
            continuing_targets=continuing_targets,
        )

        financial_budget_preview_response.additional_properties = d
        return financial_budget_preview_response

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
