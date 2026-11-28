from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_managed_execution_workflow_request_failure_policy import (
    CreateManagedExecutionWorkflowRequestFailurePolicy,
    check_create_managed_execution_workflow_request_failure_policy,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_managed_execution_workflow_step import CreateManagedExecutionWorkflowStep
    from ..models.execution_pipeline_options import ExecutionPipelineOptions
    from ..models.execution_task_batch import ExecutionTaskBatch


T = TypeVar("T", bound="CreateManagedExecutionWorkflowRequest")


@_attrs_define
class CreateManagedExecutionWorkflowRequest:
    """Encrypted, server-managed DAG. Every step runs in its own disposable
    Run. Independent steps may run in parallel up to max_parallel_steps
    and the account's concurrent Run limit. The full JSON body must not
    exceed 4 MiB. An account can have at most 16 active managed workflows.

    """

    workflow_id: str
    version: str
    max_parallel_steps: int | Unset = 1
    """Maximum number of this workflow's Runs active at once; 0 uses the default of 1."""
    failure_policy: CreateManagedExecutionWorkflowRequestFailurePolicy | Unset = "fail_fast"
    """Stop new admissions on a failed Run, or continue steps independent of failed branches."""
    pipeline: ExecutionPipelineOptions | Unset = UNSET
    """Preview; v2 only. Enables durable task steps, batch expansion and explicit retries. Submit exactly one of
    steps or batch. Requires FAAS_EXECUTION_PIPELINES_ENABLED plus v2 and tasks gates. Queueing and backoff count
    toward the deadline."""
    batch: ExecutionTaskBatch | Unset = UNSET
    """Expands into independent steps for one immutable task version. Cannot be combined with steps. Item labels
    must be unique. Account and customer/task budgets still apply to every physical attempt."""
    steps: list[CreateManagedExecutionWorkflowStep] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        workflow_id = self.workflow_id

        version = self.version

        max_parallel_steps = self.max_parallel_steps

        failure_policy: str | Unset = UNSET
        if not isinstance(self.failure_policy, Unset):
            failure_policy = self.failure_policy

        pipeline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.pipeline, Unset):
            pipeline = self.pipeline.to_dict()

        batch: dict[str, Any] | Unset = UNSET
        if not isinstance(self.batch, Unset):
            batch = self.batch.to_dict()

        steps: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.steps, Unset):
            steps = []
            for steps_item_data in self.steps:
                steps_item = steps_item_data.to_dict()
                steps.append(steps_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow_id": workflow_id,
                "version": version,
            }
        )
        if max_parallel_steps is not UNSET:
            field_dict["max_parallel_steps"] = max_parallel_steps
        if failure_policy is not UNSET:
            field_dict["failure_policy"] = failure_policy
        if pipeline is not UNSET:
            field_dict["pipeline"] = pipeline
        if batch is not UNSET:
            field_dict["batch"] = batch
        if steps is not UNSET:
            field_dict["steps"] = steps

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_managed_execution_workflow_step import CreateManagedExecutionWorkflowStep
        from ..models.execution_pipeline_options import ExecutionPipelineOptions
        from ..models.execution_task_batch import ExecutionTaskBatch

        d = dict(src_dict)
        workflow_id = d.pop("workflow_id")

        version = d.pop("version")

        max_parallel_steps = d.pop("max_parallel_steps", UNSET)

        _failure_policy = d.pop("failure_policy", UNSET)
        failure_policy: CreateManagedExecutionWorkflowRequestFailurePolicy | Unset
        if isinstance(_failure_policy, Unset):
            failure_policy = UNSET
        else:
            failure_policy = check_create_managed_execution_workflow_request_failure_policy(_failure_policy)

        _pipeline = d.pop("pipeline", UNSET)
        pipeline: ExecutionPipelineOptions | Unset
        if isinstance(_pipeline, Unset):
            pipeline = UNSET
        else:
            pipeline = ExecutionPipelineOptions.from_dict(_pipeline)

        _batch = d.pop("batch", UNSET)
        batch: ExecutionTaskBatch | Unset
        if isinstance(_batch, Unset):
            batch = UNSET
        else:
            batch = ExecutionTaskBatch.from_dict(_batch)

        _steps = d.pop("steps", UNSET)
        steps: list[CreateManagedExecutionWorkflowStep] | Unset = UNSET
        if _steps is not UNSET:
            steps = []
            for steps_item_data in _steps:
                steps_item = CreateManagedExecutionWorkflowStep.from_dict(steps_item_data)

                steps.append(steps_item)

        create_managed_execution_workflow_request = cls(
            workflow_id=workflow_id,
            version=version,
            max_parallel_steps=max_parallel_steps,
            failure_policy=failure_policy,
            pipeline=pipeline,
            batch=batch,
            steps=steps,
        )

        return create_managed_execution_workflow_request
