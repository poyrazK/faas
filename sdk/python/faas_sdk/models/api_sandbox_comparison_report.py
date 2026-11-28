from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_comparison_report_failure_side import (
    APISandboxComparisonReportFailureSide,
    check_api_sandbox_comparison_report_failure_side,
)
from ..models.api_sandbox_comparison_report_reason import (
    APISandboxComparisonReportReason,
    check_api_sandbox_comparison_report_reason,
)
from ..models.api_sandbox_comparison_report_verdict import (
    APISandboxComparisonReportVerdict,
    check_api_sandbox_comparison_report_verdict,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_run_report import APISandboxRunReport
    from ..models.api_sandbox_run_step import APISandboxRunStep


T = TypeVar("T", bound="APISandboxComparisonReport")


@_attrs_define
class APISandboxComparisonReport:
    """Paired scenario results and the verdict for a sandbox comparison."""

    id: UUID
    baseline_deployment_id: UUID
    candidate_deployment_id: UUID
    scenario_revision: UUID
    scenario_name: str
    verdict: APISandboxComparisonReportVerdict
    baseline: APISandboxRunReport
    """ Run outcome and recorded steps with their request or delivery identifiers. """
    candidate: APISandboxRunReport
    """ Run outcome and recorded steps with their request or delivery identifiers. """
    created_at: datetime.datetime
    reason: APISandboxComparisonReportReason | Unset = UNSET
    failure_side: APISandboxComparisonReportFailureSide | Unset = UNSET
    failure_code: str | Unset = UNSET
    first_failing_step: APISandboxRunStep | Unset = UNSET
    """ Redacted evidence for one request or webhook step in a scenario. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        baseline_deployment_id = str(self.baseline_deployment_id)

        candidate_deployment_id = str(self.candidate_deployment_id)

        scenario_revision = str(self.scenario_revision)

        scenario_name = self.scenario_name

        verdict: str = self.verdict

        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        created_at = self.created_at.isoformat()

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        failure_side: str | Unset = UNSET
        if not isinstance(self.failure_side, Unset):
            failure_side = self.failure_side

        failure_code = self.failure_code

        first_failing_step: dict[str, Any] | Unset = UNSET
        if not isinstance(self.first_failing_step, Unset):
            first_failing_step = self.first_failing_step.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "baseline_deployment_id": baseline_deployment_id,
                "candidate_deployment_id": candidate_deployment_id,
                "scenario_revision": scenario_revision,
                "scenario_name": scenario_name,
                "verdict": verdict,
                "baseline": baseline,
                "candidate": candidate,
                "created_at": created_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if failure_side is not UNSET:
            field_dict["failure_side"] = failure_side
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if first_failing_step is not UNSET:
            field_dict["first_failing_step"] = first_failing_step

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_run_report import APISandboxRunReport
        from ..models.api_sandbox_run_step import APISandboxRunStep

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        baseline_deployment_id = UUID(d.pop("baseline_deployment_id"))

        candidate_deployment_id = UUID(d.pop("candidate_deployment_id"))

        scenario_revision = UUID(d.pop("scenario_revision"))

        scenario_name = d.pop("scenario_name")

        verdict = check_api_sandbox_comparison_report_verdict(d.pop("verdict"))

        baseline = APISandboxRunReport.from_dict(d.pop("baseline"))

        candidate = APISandboxRunReport.from_dict(d.pop("candidate"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _reason = d.pop("reason", UNSET)
        reason: APISandboxComparisonReportReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_api_sandbox_comparison_report_reason(_reason)

        _failure_side = d.pop("failure_side", UNSET)
        failure_side: APISandboxComparisonReportFailureSide | Unset
        if isinstance(_failure_side, Unset):
            failure_side = UNSET
        else:
            failure_side = check_api_sandbox_comparison_report_failure_side(_failure_side)

        failure_code = d.pop("failure_code", UNSET)

        _first_failing_step = d.pop("first_failing_step", UNSET)
        first_failing_step: APISandboxRunStep | Unset
        if isinstance(_first_failing_step, Unset):
            first_failing_step = UNSET
        else:
            first_failing_step = APISandboxRunStep.from_dict(_first_failing_step)

        api_sandbox_comparison_report = cls(
            id=id,
            baseline_deployment_id=baseline_deployment_id,
            candidate_deployment_id=candidate_deployment_id,
            scenario_revision=scenario_revision,
            scenario_name=scenario_name,
            verdict=verdict,
            baseline=baseline,
            candidate=candidate,
            created_at=created_at,
            reason=reason,
            failure_side=failure_side,
            failure_code=failure_code,
            first_failing_step=first_failing_step,
        )

        api_sandbox_comparison_report.additional_properties = d
        return api_sandbox_comparison_report

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
