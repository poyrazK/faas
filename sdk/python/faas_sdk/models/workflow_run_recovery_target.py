from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRunRecoveryTarget")


@_attrs_define
class WorkflowRunRecoveryTarget:
    """One reviewed workflow run and resume count. Retry children identify the saved parent item. Initial failure targets
    may identify retained generation-zero evidence, which is checked again at creation and admission.

    """

    run_id: UUID
    expected_resume_count: int
    failure_evidence_id: UUID | Unset = UNSET
    """Optional captured initial failure identity. Requires expected_resume_count zero and no recovery parent. Must
    match retained app, workflow, run and failure status."""
    parent_position: int | Unset = UNSET
    """Required only with parent_job_id. Unique position of the queued parent item whose saved failed outcome
    matches this run and resume count."""

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        expected_resume_count = self.expected_resume_count

        failure_evidence_id: str | Unset = UNSET
        if not isinstance(self.failure_evidence_id, Unset):
            failure_evidence_id = str(self.failure_evidence_id)

        parent_position = self.parent_position

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "run_id": run_id,
                "expected_resume_count": expected_resume_count,
            }
        )
        if failure_evidence_id is not UNSET:
            field_dict["failure_evidence_id"] = failure_evidence_id
        if parent_position is not UNSET:
            field_dict["parent_position"] = parent_position

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        expected_resume_count = d.pop("expected_resume_count")

        _failure_evidence_id = d.pop("failure_evidence_id", UNSET)
        failure_evidence_id: UUID | Unset
        if isinstance(_failure_evidence_id, Unset):
            failure_evidence_id = UNSET
        else:
            failure_evidence_id = UUID(_failure_evidence_id)

        parent_position = d.pop("parent_position", UNSET)

        workflow_run_recovery_target = cls(
            run_id=run_id,
            expected_resume_count=expected_resume_count,
            failure_evidence_id=failure_evidence_id,
            parent_position=parent_position,
        )

        return workflow_run_recovery_target
