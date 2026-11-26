from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_suite_run_report_failure_code import (
    APISandboxSuiteRunReportFailureCode,
    check_api_sandbox_suite_run_report_failure_code,
)
from ..models.api_sandbox_suite_run_report_stale_reason import (
    APISandboxSuiteRunReportStaleReason,
    check_api_sandbox_suite_run_report_stale_reason,
)
from ..models.api_sandbox_suite_run_report_verdict import (
    APISandboxSuiteRunReportVerdict,
    check_api_sandbox_suite_run_report_verdict,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_suite_scenario_result import APISandboxSuiteScenarioResult


T = TypeVar("T", bound="APISandboxSuiteRunReport")


@_attrs_define
class APISandboxSuiteRunReport:
    id: UUID
    suite_revision: UUID
    baseline_deployment_id: UUID
    candidate_deployment_id: UUID
    scenario_count: int
    completed_count: int
    created_at: datetime.datetime
    verdict: APISandboxSuiteRunReportVerdict
    scenarios: list[APISandboxSuiteScenarioResult]
    automatic: bool | Unset = UNSET
    """This run started automatically at release approval."""
    waiting_for_capacity: bool | Unset = UNSET
    """The next comparison is waiting for available sandbox capacity."""
    finished_at: datetime.datetime | Unset = UNSET
    stale_reason: APISandboxSuiteRunReportStaleReason | Unset = UNSET
    failure_code: APISandboxSuiteRunReportFailureCode | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        suite_revision = str(self.suite_revision)

        baseline_deployment_id = str(self.baseline_deployment_id)

        candidate_deployment_id = str(self.candidate_deployment_id)

        scenario_count = self.scenario_count

        completed_count = self.completed_count

        created_at = self.created_at.isoformat()

        verdict: str = self.verdict

        scenarios = []
        for scenarios_item_data in self.scenarios:
            scenarios_item = scenarios_item_data.to_dict()
            scenarios.append(scenarios_item)

        automatic = self.automatic

        waiting_for_capacity = self.waiting_for_capacity

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        stale_reason: str | Unset = UNSET
        if not isinstance(self.stale_reason, Unset):
            stale_reason = self.stale_reason

        failure_code: str | Unset = UNSET
        if not isinstance(self.failure_code, Unset):
            failure_code = self.failure_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "suite_revision": suite_revision,
                "baseline_deployment_id": baseline_deployment_id,
                "candidate_deployment_id": candidate_deployment_id,
                "scenario_count": scenario_count,
                "completed_count": completed_count,
                "created_at": created_at,
                "verdict": verdict,
                "scenarios": scenarios,
            }
        )
        if automatic is not UNSET:
            field_dict["automatic"] = automatic
        if waiting_for_capacity is not UNSET:
            field_dict["waiting_for_capacity"] = waiting_for_capacity
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if stale_reason is not UNSET:
            field_dict["stale_reason"] = stale_reason
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_suite_scenario_result import APISandboxSuiteScenarioResult

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        suite_revision = UUID(d.pop("suite_revision"))

        baseline_deployment_id = UUID(d.pop("baseline_deployment_id"))

        candidate_deployment_id = UUID(d.pop("candidate_deployment_id"))

        scenario_count = d.pop("scenario_count")

        completed_count = d.pop("completed_count")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        verdict = check_api_sandbox_suite_run_report_verdict(d.pop("verdict"))

        scenarios = []
        _scenarios = d.pop("scenarios")
        for scenarios_item_data in _scenarios:
            scenarios_item = APISandboxSuiteScenarioResult.from_dict(scenarios_item_data)

            scenarios.append(scenarios_item)

        automatic = d.pop("automatic", UNSET)

        waiting_for_capacity = d.pop("waiting_for_capacity", UNSET)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        _stale_reason = d.pop("stale_reason", UNSET)
        stale_reason: APISandboxSuiteRunReportStaleReason | Unset
        if isinstance(_stale_reason, Unset):
            stale_reason = UNSET
        else:
            stale_reason = check_api_sandbox_suite_run_report_stale_reason(_stale_reason)

        _failure_code = d.pop("failure_code", UNSET)
        failure_code: APISandboxSuiteRunReportFailureCode | Unset
        if isinstance(_failure_code, Unset):
            failure_code = UNSET
        else:
            failure_code = check_api_sandbox_suite_run_report_failure_code(_failure_code)

        api_sandbox_suite_run_report = cls(
            id=id,
            suite_revision=suite_revision,
            baseline_deployment_id=baseline_deployment_id,
            candidate_deployment_id=candidate_deployment_id,
            scenario_count=scenario_count,
            completed_count=completed_count,
            created_at=created_at,
            verdict=verdict,
            scenarios=scenarios,
            automatic=automatic,
            waiting_for_capacity=waiting_for_capacity,
            finished_at=finished_at,
            stale_reason=stale_reason,
            failure_code=failure_code,
        )

        api_sandbox_suite_run_report.additional_properties = d
        return api_sandbox_suite_run_report

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
