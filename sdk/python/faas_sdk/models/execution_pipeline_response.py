from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_pipeline_completion import ExecutionPipelineCompletion
    from ..models.execution_pipeline_progress import ExecutionPipelineProgress
    from ..models.execution_pipeline_rerun import ExecutionPipelineRerun
    from ..models.execution_pipeline_step_response import ExecutionPipelineStepResponse


T = TypeVar("T", bound="ExecutionPipelineResponse")


@_attrs_define
class ExecutionPipelineResponse:
    """Managed task pipeline state, steps and available completion details."""

    deadline_at: datetime.datetime
    steps: list[ExecutionPipelineStepResponse]
    progress: ExecutionPipelineProgress | Unset = UNSET
    """Current pipeline sequence and observed progress for each step."""
    rerun: ExecutionPipelineRerun | Unset = UNSET
    """Rerun receipt linking the replacement pipeline to its original job and reused outputs."""
    completion: ExecutionPipelineCompletion | Unset = UNSET
    """Immutable v1 terminal manifest. Metadata and scoped references only; result and artifact bytes retain their
    ordinary Run receipt permissions and expiry. Completion identity is stable across delivery retries and redrive.
    Aggregate wall/CPU time sums every attempt; peak memory is the maximum attempt peak, not simultaneous residency
    or an invoice."""
    cancel_requested: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deadline_at = self.deadline_at.isoformat()

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.progress, Unset):
            progress = self.progress.to_dict()

        rerun: dict[str, Any] | Unset = UNSET
        if not isinstance(self.rerun, Unset):
            rerun = self.rerun.to_dict()

        completion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.completion, Unset):
            completion = self.completion.to_dict()

        cancel_requested = self.cancel_requested

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deadline_at": deadline_at,
                "steps": steps,
            }
        )
        if progress is not UNSET:
            field_dict["progress"] = progress
        if rerun is not UNSET:
            field_dict["rerun"] = rerun
        if completion is not UNSET:
            field_dict["completion"] = completion
        if cancel_requested is not UNSET:
            field_dict["cancel_requested"] = cancel_requested

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_completion import ExecutionPipelineCompletion
        from ..models.execution_pipeline_progress import ExecutionPipelineProgress
        from ..models.execution_pipeline_rerun import ExecutionPipelineRerun
        from ..models.execution_pipeline_step_response import ExecutionPipelineStepResponse

        d = dict(src_dict)
        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = ExecutionPipelineStepResponse.from_dict(steps_item_data)

            steps.append(steps_item)

        _progress = d.pop("progress", UNSET)
        progress: ExecutionPipelineProgress | Unset
        if isinstance(_progress, Unset):
            progress = UNSET
        else:
            progress = ExecutionPipelineProgress.from_dict(_progress)

        _rerun = d.pop("rerun", UNSET)
        rerun: ExecutionPipelineRerun | Unset
        if isinstance(_rerun, Unset):
            rerun = UNSET
        else:
            rerun = ExecutionPipelineRerun.from_dict(_rerun)

        _completion = d.pop("completion", UNSET)
        completion: ExecutionPipelineCompletion | Unset
        if isinstance(_completion, Unset):
            completion = UNSET
        else:
            completion = ExecutionPipelineCompletion.from_dict(_completion)

        cancel_requested = d.pop("cancel_requested", UNSET)

        execution_pipeline_response = cls(
            deadline_at=deadline_at,
            steps=steps,
            progress=progress,
            rerun=rerun,
            completion=completion,
            cancel_requested=cancel_requested,
        )

        execution_pipeline_response.additional_properties = d
        return execution_pipeline_response

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
