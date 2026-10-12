from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRunRecoveryTarget")


@_attrs_define
class WorkflowRunRecoveryTarget:
    """One reviewed workflow run and resume count. Retry children also identify the saved parent item."""

    run_id: UUID
    expected_resume_count: int
    parent_position: int | Unset = UNSET
    """Required only with parent_job_id. Unique position of the queued parent item whose saved failed outcome
    matches this run and resume count."""

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        expected_resume_count = self.expected_resume_count

        parent_position = self.parent_position

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "run_id": run_id,
                "expected_resume_count": expected_resume_count,
            }
        )
        if parent_position is not UNSET:
            field_dict["parent_position"] = parent_position

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        expected_resume_count = d.pop("expected_resume_count")

        parent_position = d.pop("parent_position", UNSET)

        workflow_run_recovery_target = cls(
            run_id=run_id,
            expected_resume_count=expected_resume_count,
            parent_position=parent_position,
        )

        return workflow_run_recovery_target
