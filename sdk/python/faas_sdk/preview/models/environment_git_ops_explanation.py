from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_git_ops_explanation_source_freshness import (
    EnvironmentGitOpsExplanationSourceFreshness,
    check_environment_git_ops_explanation_source_freshness,
)
from ..models.environment_git_ops_explanation_status import (
    EnvironmentGitOpsExplanationStatus,
    check_environment_git_ops_explanation_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_ops_diagnostic import EnvironmentGitOpsDiagnostic
    from ..models.environment_workload_progress import EnvironmentWorkloadProgress


T = TypeVar("T", bound="EnvironmentGitOpsExplanation")


@_attrs_define
class EnvironmentGitOpsExplanation:
    """Read-only assessment of recorded source, graph and workload facts. Unknown or historical evidence never grants
    readiness or execution authority.

    """

    read_only: bool
    assessed_at: datetime.datetime
    status: EnvironmentGitOpsExplanationStatus
    summary: str
    source_id: str
    generation: int
    intent_version: int
    source_freshness: EnvironmentGitOpsExplanationSourceFreshness
    issues: list[EnvironmentGitOpsDiagnostic]
    workloads: list[EnvironmentWorkloadProgress]
    observed_commit_sha: str | Unset = UNSET
    approved_revision_id: str | Unset = UNSET
    applied_revision_id: str | Unset = UNSET
    serving_revision_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        read_only = self.read_only

        assessed_at = self.assessed_at.isoformat()

        status: str = self.status

        summary = self.summary

        source_id = self.source_id

        generation = self.generation

        intent_version = self.intent_version

        source_freshness: str = self.source_freshness

        issues = []
        for issues_item_data in self.issues:
            issues_item = issues_item_data.to_dict()
            issues.append(issues_item)

        workloads = []
        for workloads_item_data in self.workloads:
            workloads_item = workloads_item_data.to_dict()
            workloads.append(workloads_item)

        observed_commit_sha = self.observed_commit_sha

        approved_revision_id = self.approved_revision_id

        applied_revision_id = self.applied_revision_id

        serving_revision_id = self.serving_revision_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "read_only": read_only,
                "assessed_at": assessed_at,
                "status": status,
                "summary": summary,
                "source_id": source_id,
                "generation": generation,
                "intent_version": intent_version,
                "source_freshness": source_freshness,
                "issues": issues,
                "workloads": workloads,
            }
        )
        if observed_commit_sha is not UNSET:
            field_dict["observed_commit_sha"] = observed_commit_sha
        if approved_revision_id is not UNSET:
            field_dict["approved_revision_id"] = approved_revision_id
        if applied_revision_id is not UNSET:
            field_dict["applied_revision_id"] = applied_revision_id
        if serving_revision_id is not UNSET:
            field_dict["serving_revision_id"] = serving_revision_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_ops_diagnostic import EnvironmentGitOpsDiagnostic
        from ..models.environment_workload_progress import EnvironmentWorkloadProgress

        d = dict(src_dict)
        read_only = d.pop("read_only")

        assessed_at = datetime.datetime.fromisoformat(d.pop("assessed_at"))

        status = check_environment_git_ops_explanation_status(d.pop("status"))

        summary = d.pop("summary")

        source_id = d.pop("source_id")

        generation = d.pop("generation")

        intent_version = d.pop("intent_version")

        source_freshness = check_environment_git_ops_explanation_source_freshness(d.pop("source_freshness"))

        issues = []
        _issues = d.pop("issues")
        for issues_item_data in _issues:
            issues_item = EnvironmentGitOpsDiagnostic.from_dict(issues_item_data)

            issues.append(issues_item)

        workloads = []
        _workloads = d.pop("workloads")
        for workloads_item_data in _workloads:
            workloads_item = EnvironmentWorkloadProgress.from_dict(workloads_item_data)

            workloads.append(workloads_item)

        observed_commit_sha = d.pop("observed_commit_sha", UNSET)

        approved_revision_id = d.pop("approved_revision_id", UNSET)

        applied_revision_id = d.pop("applied_revision_id", UNSET)

        serving_revision_id = d.pop("serving_revision_id", UNSET)

        environment_git_ops_explanation = cls(
            read_only=read_only,
            assessed_at=assessed_at,
            status=status,
            summary=summary,
            source_id=source_id,
            generation=generation,
            intent_version=intent_version,
            source_freshness=source_freshness,
            issues=issues,
            workloads=workloads,
            observed_commit_sha=observed_commit_sha,
            approved_revision_id=approved_revision_id,
            applied_revision_id=applied_revision_id,
            serving_revision_id=serving_revision_id,
        )

        environment_git_ops_explanation.additional_properties = d
        return environment_git_ops_explanation

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
