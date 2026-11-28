from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_pipeline_completed_step_outcome import (
    ExecutionPipelineCompletedStepOutcome,
    check_execution_pipeline_completed_step_outcome,
)
from ..models.execution_pipeline_completed_step_result_validation import (
    ExecutionPipelineCompletedStepResultValidation,
    check_execution_pipeline_completed_step_result_validation,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_pipeline_completed_attempt import ExecutionPipelineCompletedAttempt
    from ..models.execution_task_ref import ExecutionTaskRef


T = TypeVar("T", bound="ExecutionPipelineCompletedStep")


@_attrs_define
class ExecutionPipelineCompletedStep:
    """A terminal step outcome with attempts and retained output references."""

    label: str
    outcome: ExecutionPipelineCompletedStepOutcome
    attempts: list[ExecutionPipelineCompletedAttempt]
    reused_run_id: UUID | Unset = UNSET
    output_sha256: str | Unset = UNSET
    task: ExecutionTaskRef | Unset = UNSET
    """ Identity of a published executable task version. """
    result_validation: ExecutionPipelineCompletedStepResultValidation | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        outcome: str = self.outcome

        attempts = []
        for attempts_item_data in self.attempts:
            attempts_item = attempts_item_data.to_dict()
            attempts.append(attempts_item)

        reused_run_id: str | Unset = UNSET
        if not isinstance(self.reused_run_id, Unset):
            reused_run_id = str(self.reused_run_id)

        output_sha256 = self.output_sha256

        task: dict[str, Any] | Unset = UNSET
        if not isinstance(self.task, Unset):
            task = self.task.to_dict()

        result_validation: str | Unset = UNSET
        if not isinstance(self.result_validation, Unset):
            result_validation = self.result_validation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "label": label,
                "outcome": outcome,
                "attempts": attempts,
            }
        )
        if reused_run_id is not UNSET:
            field_dict["reused_run_id"] = reused_run_id
        if output_sha256 is not UNSET:
            field_dict["output_sha256"] = output_sha256
        if task is not UNSET:
            field_dict["task"] = task
        if result_validation is not UNSET:
            field_dict["result_validation"] = result_validation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_completed_attempt import ExecutionPipelineCompletedAttempt
        from ..models.execution_task_ref import ExecutionTaskRef

        d = dict(src_dict)
        label = d.pop("label")

        outcome = check_execution_pipeline_completed_step_outcome(d.pop("outcome"))

        attempts = []
        _attempts = d.pop("attempts")
        for attempts_item_data in _attempts:
            attempts_item = ExecutionPipelineCompletedAttempt.from_dict(attempts_item_data)

            attempts.append(attempts_item)

        _reused_run_id = d.pop("reused_run_id", UNSET)
        reused_run_id: UUID | Unset
        if isinstance(_reused_run_id, Unset):
            reused_run_id = UNSET
        else:
            reused_run_id = UUID(_reused_run_id)

        output_sha256 = d.pop("output_sha256", UNSET)

        _task = d.pop("task", UNSET)
        task: ExecutionTaskRef | Unset
        if isinstance(_task, Unset):
            task = UNSET
        else:
            task = ExecutionTaskRef.from_dict(_task)

        _result_validation = d.pop("result_validation", UNSET)
        result_validation: ExecutionPipelineCompletedStepResultValidation | Unset
        if isinstance(_result_validation, Unset):
            result_validation = UNSET
        else:
            result_validation = check_execution_pipeline_completed_step_result_validation(_result_validation)

        execution_pipeline_completed_step = cls(
            label=label,
            outcome=outcome,
            attempts=attempts,
            reused_run_id=reused_run_id,
            output_sha256=output_sha256,
            task=task,
            result_validation=result_validation,
        )

        execution_pipeline_completed_step.additional_properties = d
        return execution_pipeline_completed_step

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
