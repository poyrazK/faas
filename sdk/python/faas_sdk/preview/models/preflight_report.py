from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.preflight_plan_budget import PreflightPlanBudget
    from ..models.preflight_source import PreflightSource
    from ..models.preflight_verdict import PreflightVerdict


T = TypeVar("T", bound="PreflightReport")


@_attrs_define
class PreflightReport:
    """One complete preflight answer, pinned to the commit it was computed
    from so a permalink always re-renders the same verdict.

    """

    source: PreflightSource
    """A validated public GitHub repository reference. Every field has passed
    the character and length rules, so it is safe to interpolate upstream.
    """
    commit_sha: str
    """The commit the verdict was computed from."""
    verdict: PreflightVerdict
    """The assessed result for one source tree: a headline level, the findings
    behind it, and the run contract that was inferred.
    """
    plan_budgets: list[PreflightPlanBudget]
    checked_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = self.source.to_dict()

        commit_sha = self.commit_sha

        verdict = self.verdict.to_dict()

        plan_budgets = []
        for plan_budgets_item_data in self.plan_budgets:
            plan_budgets_item = plan_budgets_item_data.to_dict()
            plan_budgets.append(plan_budgets_item)

        checked_at = self.checked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "commit_sha": commit_sha,
                "verdict": verdict,
                "plan_budgets": plan_budgets,
                "checked_at": checked_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.preflight_plan_budget import PreflightPlanBudget
        from ..models.preflight_source import PreflightSource
        from ..models.preflight_verdict import PreflightVerdict

        d = dict(src_dict)
        source = PreflightSource.from_dict(d.pop("source"))

        commit_sha = d.pop("commit_sha")

        verdict = PreflightVerdict.from_dict(d.pop("verdict"))

        plan_budgets = []
        _plan_budgets = d.pop("plan_budgets")
        for plan_budgets_item_data in _plan_budgets:
            plan_budgets_item = PreflightPlanBudget.from_dict(plan_budgets_item_data)

            plan_budgets.append(plan_budgets_item)

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        preflight_report = cls(
            source=source,
            commit_sha=commit_sha,
            verdict=verdict,
            plan_budgets=plan_budgets,
            checked_at=checked_at,
        )

        preflight_report.additional_properties = d
        return preflight_report

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
