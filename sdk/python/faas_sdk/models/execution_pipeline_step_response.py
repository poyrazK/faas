from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_pipeline_attempt_response import ExecutionPipelineAttemptResponse
    from ..models.execution_task_ref import ExecutionTaskRef


T = TypeVar("T", bound="ExecutionPipelineStepResponse")


@_attrs_define
class ExecutionPipelineStepResponse:
    """One declared task step with its dependencies and execution attempts."""

    label: str
    max_attempts: int
    attempts: list[ExecutionPipelineAttemptResponse]
    task: ExecutionTaskRef | Unset = UNSET
    """ Identity of a published executable task version. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        max_attempts = self.max_attempts

        attempts = []
        for attempts_item_data in self.attempts:
            attempts_item = attempts_item_data.to_dict()
            attempts.append(attempts_item)

        task: dict[str, Any] | Unset = UNSET
        if not isinstance(self.task, Unset):
            task = self.task.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "label": label,
                "max_attempts": max_attempts,
                "attempts": attempts,
            }
        )
        if task is not UNSET:
            field_dict["task"] = task

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_attempt_response import ExecutionPipelineAttemptResponse
        from ..models.execution_task_ref import ExecutionTaskRef

        d = dict(src_dict)
        label = d.pop("label")

        max_attempts = d.pop("max_attempts")

        attempts = []
        _attempts = d.pop("attempts")
        for attempts_item_data in _attempts:
            attempts_item = ExecutionPipelineAttemptResponse.from_dict(attempts_item_data)

            attempts.append(attempts_item)

        _task = d.pop("task", UNSET)
        task: ExecutionTaskRef | Unset
        if isinstance(_task, Unset):
            task = UNSET
        else:
            task = ExecutionTaskRef.from_dict(_task)

        execution_pipeline_step_response = cls(
            label=label,
            max_attempts=max_attempts,
            attempts=attempts,
            task=task,
        )

        execution_pipeline_step_response.additional_properties = d
        return execution_pipeline_step_response

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
