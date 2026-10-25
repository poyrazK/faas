from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_run_recovery_target import WorkflowRunRecoveryTarget


T = TypeVar("T", bound="WorkflowRunRecoveryPlan")


@_attrs_define
class WorkflowRunRecoveryPlan:
    """Explicit reviewed native workflow runs. Requires workflow mode and request_id; excludes event selections and receipt
    protection. Each resume checks the original count and eligibility atomically with its job outcome. Concurrency
    pressure leaves the item pending; changed or unsafe targets are skipped. Queued means a resume was admitted, not
    execution success. No new runs are selected. The original normalized request returns the retained job on retry;
    changes conflict.

    """

    name: str
    runs: list[WorkflowRunRecoveryTarget]
    execution_deadline_seconds: int | Unset = UNSET
    """Advisory deadline from each admission. Zero disables it; nonzero values must be at least 60 seconds."""
    execution_stall_seconds: int | Unset = UNSET
    """No observable progress threshold. Zero uses 900 seconds; nonzero values must be at least 60 seconds.
    Scheduled and capacity waits do not trigger stalled alerts."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        runs = []
        for runs_item_data in self.runs:
            runs_item = runs_item_data.to_dict()
            runs.append(runs_item)

        execution_deadline_seconds = self.execution_deadline_seconds

        execution_stall_seconds = self.execution_stall_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "runs": runs,
            }
        )
        if execution_deadline_seconds is not UNSET:
            field_dict["execution_deadline_seconds"] = execution_deadline_seconds
        if execution_stall_seconds is not UNSET:
            field_dict["execution_stall_seconds"] = execution_stall_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_run_recovery_target import WorkflowRunRecoveryTarget

        d = dict(src_dict)
        name = d.pop("name")

        runs = []
        _runs = d.pop("runs")
        for runs_item_data in _runs:
            runs_item = WorkflowRunRecoveryTarget.from_dict(runs_item_data)

            runs.append(runs_item)

        execution_deadline_seconds = d.pop("execution_deadline_seconds", UNSET)

        execution_stall_seconds = d.pop("execution_stall_seconds", UNSET)

        workflow_run_recovery_plan = cls(
            name=name,
            runs=runs,
            execution_deadline_seconds=execution_deadline_seconds,
            execution_stall_seconds=execution_stall_seconds,
        )

        return workflow_run_recovery_plan
