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
    steps: list[CreateManagedExecutionWorkflowStep]
    max_parallel_steps: int | Unset = 1
    """Maximum number of this workflow's Runs active at once; 0 uses the default of 1."""
    failure_policy: CreateManagedExecutionWorkflowRequestFailurePolicy | Unset = "fail_fast"
    """Stop new admissions on a failed Run, or continue steps independent of failed branches."""

    def to_dict(self) -> dict[str, Any]:
        workflow_id = self.workflow_id

        version = self.version

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        max_parallel_steps = self.max_parallel_steps

        failure_policy: str | Unset = UNSET
        if not isinstance(self.failure_policy, Unset):
            failure_policy = self.failure_policy

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow_id": workflow_id,
                "version": version,
                "steps": steps,
            }
        )
        if max_parallel_steps is not UNSET:
            field_dict["max_parallel_steps"] = max_parallel_steps
        if failure_policy is not UNSET:
            field_dict["failure_policy"] = failure_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_managed_execution_workflow_step import CreateManagedExecutionWorkflowStep

        d = dict(src_dict)
        workflow_id = d.pop("workflow_id")

        version = d.pop("version")

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = CreateManagedExecutionWorkflowStep.from_dict(steps_item_data)

            steps.append(steps_item)

        max_parallel_steps = d.pop("max_parallel_steps", UNSET)

        _failure_policy = d.pop("failure_policy", UNSET)
        failure_policy: CreateManagedExecutionWorkflowRequestFailurePolicy | Unset
        if isinstance(_failure_policy, Unset):
            failure_policy = UNSET
        else:
            failure_policy = check_create_managed_execution_workflow_request_failure_policy(_failure_policy)

        create_managed_execution_workflow_request = cls(
            workflow_id=workflow_id,
            version=version,
            steps=steps,
            max_parallel_steps=max_parallel_steps,
            failure_policy=failure_policy,
        )

        return create_managed_execution_workflow_request
