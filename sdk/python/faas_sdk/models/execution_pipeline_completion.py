from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_pipeline_completion_outcome import (
    ExecutionPipelineCompletionOutcome,
    check_execution_pipeline_completion_outcome,
)
from ..models.execution_pipeline_completion_version import (
    ExecutionPipelineCompletionVersion,
    check_execution_pipeline_completion_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_pipeline_completed_step import ExecutionPipelineCompletedStep
    from ..models.execution_pipeline_rerun import ExecutionPipelineRerun
    from ..models.execution_usage import ExecutionUsage


T = TypeVar("T", bound="ExecutionPipelineCompletion")


@_attrs_define
class ExecutionPipelineCompletion:
    """Immutable v1 terminal manifest. Metadata and scoped references only; result and artifact bytes retain their ordinary
    Run receipt permissions and expiry. Completion identity is stable across delivery retries and redrive. Aggregate
    wall/CPU time sums every attempt; peak memory is the maximum attempt peak, not simultaneous residency or an invoice.

    """

    version: ExecutionPipelineCompletionVersion
    completion_id: UUID
    workflow_id: str
    outcome: ExecutionPipelineCompletionOutcome
    finished_at: datetime.datetime
    receipt: str
    usage: ExecutionUsage
    """ Host-measured resource usage for a terminal execution. """
    steps: list[ExecutionPipelineCompletedStep]
    rerun: ExecutionPipelineRerun | Unset = UNSET
    """ Rerun receipt linking the replacement pipeline to its original job and reused outputs. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: str = self.version

        completion_id = str(self.completion_id)

        workflow_id = self.workflow_id

        outcome: str = self.outcome

        finished_at = self.finished_at.isoformat()

        receipt = self.receipt

        usage = self.usage.to_dict()

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        rerun: dict[str, Any] | Unset = UNSET
        if not isinstance(self.rerun, Unset):
            rerun = self.rerun.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "completion_id": completion_id,
                "workflow_id": workflow_id,
                "outcome": outcome,
                "finished_at": finished_at,
                "receipt": receipt,
                "usage": usage,
                "steps": steps,
            }
        )
        if rerun is not UNSET:
            field_dict["rerun"] = rerun

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_completed_step import ExecutionPipelineCompletedStep
        from ..models.execution_pipeline_rerun import ExecutionPipelineRerun
        from ..models.execution_usage import ExecutionUsage

        d = dict(src_dict)
        version = check_execution_pipeline_completion_version(d.pop("version"))

        completion_id = UUID(d.pop("completion_id"))

        workflow_id = d.pop("workflow_id")

        outcome = check_execution_pipeline_completion_outcome(d.pop("outcome"))

        finished_at = datetime.datetime.fromisoformat(d.pop("finished_at"))

        receipt = d.pop("receipt")

        usage = ExecutionUsage.from_dict(d.pop("usage"))

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = ExecutionPipelineCompletedStep.from_dict(steps_item_data)

            steps.append(steps_item)

        _rerun = d.pop("rerun", UNSET)
        rerun: ExecutionPipelineRerun | Unset
        if isinstance(_rerun, Unset):
            rerun = UNSET
        else:
            rerun = ExecutionPipelineRerun.from_dict(_rerun)

        execution_pipeline_completion = cls(
            version=version,
            completion_id=completion_id,
            workflow_id=workflow_id,
            outcome=outcome,
            finished_at=finished_at,
            receipt=receipt,
            usage=usage,
            steps=steps,
            rerun=rerun,
        )

        execution_pipeline_completion.additional_properties = d
        return execution_pipeline_completion

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
