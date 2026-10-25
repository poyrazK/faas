from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_ops_explanation import EnvironmentGitOpsExplanation
    from ..models.environment_git_ops_run import EnvironmentGitOpsRun
    from ..models.environment_git_revision_approval import EnvironmentGitRevisionApproval
    from ..models.environment_git_source import EnvironmentGitSource
    from ..models.environment_workload_activation_evidence import EnvironmentWorkloadActivationEvidence
    from ..models.environment_workload_retirement import EnvironmentWorkloadRetirement


T = TypeVar("T", bound="EnvironmentGitOpsStatusResponse")


@_attrs_define
class EnvironmentGitOpsStatusResponse:
    """Source authority, current workload qualification evidence, and the twenty most recent durable reconciliation
    attempts.

    """

    source: EnvironmentGitSource
    """Durable environment authority with separate approved and fully applied revision pointers."""
    runs: list[EnvironmentGitOpsRun]
    explanation: EnvironmentGitOpsExplanation | Unset = UNSET
    """Read-only assessment of recorded source, graph and workload facts. Unknown or historical evidence never
    grants readiness or execution authority."""
    approval: EnvironmentGitRevisionApproval | Unset = UNSET
    """Immutable reviewed-merge evidence bound to the approved definition and source generation."""
    workload_retirements: list[EnvironmentWorkloadRetirement] | Unset = UNSET
    workload_evidence: EnvironmentWorkloadActivationEvidence | Unset = UNSET
    """Evidence summary for the exact current reviewed workload graph; retained workloads count only when their
    exact live deployment remains in the active release set. This evidence does not authorize candidate activation
    or serving."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = self.source.to_dict()

        runs = []
        for runs_item_data in self.runs:
            runs_item = runs_item_data.to_dict()
            runs.append(runs_item)

        explanation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.explanation, Unset):
            explanation = self.explanation.to_dict()

        approval: dict[str, Any] | Unset = UNSET
        if not isinstance(self.approval, Unset):
            approval = self.approval.to_dict()

        workload_retirements: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workload_retirements, Unset):
            workload_retirements = []
            for workload_retirements_item_data in self.workload_retirements:
                workload_retirements_item = workload_retirements_item_data.to_dict()
                workload_retirements.append(workload_retirements_item)

        workload_evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.workload_evidence, Unset):
            workload_evidence = self.workload_evidence.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "runs": runs,
            }
        )
        if explanation is not UNSET:
            field_dict["explanation"] = explanation
        if approval is not UNSET:
            field_dict["approval"] = approval
        if workload_retirements is not UNSET:
            field_dict["workload_retirements"] = workload_retirements
        if workload_evidence is not UNSET:
            field_dict["workload_evidence"] = workload_evidence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_ops_explanation import EnvironmentGitOpsExplanation
        from ..models.environment_git_ops_run import EnvironmentGitOpsRun
        from ..models.environment_git_revision_approval import EnvironmentGitRevisionApproval
        from ..models.environment_git_source import EnvironmentGitSource
        from ..models.environment_workload_activation_evidence import EnvironmentWorkloadActivationEvidence
        from ..models.environment_workload_retirement import EnvironmentWorkloadRetirement

        d = dict(src_dict)
        source = EnvironmentGitSource.from_dict(d.pop("source"))

        runs = []
        _runs = d.pop("runs")
        for runs_item_data in _runs:
            runs_item = EnvironmentGitOpsRun.from_dict(runs_item_data)

            runs.append(runs_item)

        _explanation = d.pop("explanation", UNSET)
        explanation: EnvironmentGitOpsExplanation | Unset
        if isinstance(_explanation, Unset):
            explanation = UNSET
        else:
            explanation = EnvironmentGitOpsExplanation.from_dict(_explanation)

        _approval = d.pop("approval", UNSET)
        approval: EnvironmentGitRevisionApproval | Unset
        if isinstance(_approval, Unset):
            approval = UNSET
        else:
            approval = EnvironmentGitRevisionApproval.from_dict(_approval)

        _workload_retirements = d.pop("workload_retirements", UNSET)
        workload_retirements: list[EnvironmentWorkloadRetirement] | Unset = UNSET
        if _workload_retirements is not UNSET:
            workload_retirements = []
            for workload_retirements_item_data in _workload_retirements:
                workload_retirements_item = EnvironmentWorkloadRetirement.from_dict(workload_retirements_item_data)

                workload_retirements.append(workload_retirements_item)

        _workload_evidence = d.pop("workload_evidence", UNSET)
        workload_evidence: EnvironmentWorkloadActivationEvidence | Unset
        if isinstance(_workload_evidence, Unset):
            workload_evidence = UNSET
        else:
            workload_evidence = EnvironmentWorkloadActivationEvidence.from_dict(_workload_evidence)

        environment_git_ops_status_response = cls(
            source=source,
            runs=runs,
            explanation=explanation,
            approval=approval,
            workload_retirements=workload_retirements,
            workload_evidence=workload_evidence,
        )

        environment_git_ops_status_response.additional_properties = d
        return environment_git_ops_status_response

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
