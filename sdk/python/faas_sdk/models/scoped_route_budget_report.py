from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scoped_route_budget_report_budget_source import (
    ScopedRouteBudgetReportBudgetSource,
    check_scoped_route_budget_report_budget_source,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_report import RouteMonitorReport


T = TypeVar("T", bound="ScopedRouteBudgetReport")


@_attrs_define
class ScopedRouteBudgetReport:
    """Read-only evaluation of inherited or overridden budgets against one selected environment serving release. Completed
    current windows only; no pooled history, customer cohorts, scoped incidents or automatic rollback.

    """

    scope: str
    budget_source: ScopedRouteBudgetReportBudgetSource
    report: RouteMonitorReport
    """Read-only absolute-budget evidence for the sole fully serving default-scope deployment. Coverage is limited
    to stored telemetry."""
    project_id: str | Unset = UNSET
    environment_id: str | Unset = UNSET
    budget_revision: int | Unset = UNSET
    app_budget_revision: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        budget_source: str = self.budget_source

        report = self.report.to_dict()

        project_id = self.project_id

        environment_id = self.environment_id

        budget_revision = self.budget_revision

        app_budget_revision = self.app_budget_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "budget_source": budget_source,
                "report": report,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if budget_revision is not UNSET:
            field_dict["budget_revision"] = budget_revision
        if app_budget_revision is not UNSET:
            field_dict["app_budget_revision"] = app_budget_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_report import RouteMonitorReport

        d = dict(src_dict)
        scope = d.pop("scope")

        budget_source = check_scoped_route_budget_report_budget_source(d.pop("budget_source"))

        report = RouteMonitorReport.from_dict(d.pop("report"))

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        budget_revision = d.pop("budget_revision", UNSET)

        app_budget_revision = d.pop("app_budget_revision", UNSET)

        scoped_route_budget_report = cls(
            scope=scope,
            budget_source=budget_source,
            report=report,
            project_id=project_id,
            environment_id=environment_id,
            budget_revision=budget_revision,
            app_budget_revision=app_budget_revision,
        )

        scoped_route_budget_report.additional_properties = d
        return scoped_route_budget_report

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
