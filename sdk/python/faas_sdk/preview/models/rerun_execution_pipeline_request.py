from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_managed_execution_workflow_request import CreateManagedExecutionWorkflowRequest


T = TypeVar("T", bound="RerunExecutionPipelineRequest")


@_attrs_define
class RerunExecutionPipelineRequest:
    """New workflow identity and acknowledgement of previously started steps that may repeat."""

    workflow_id: str
    original: CreateManagedExecutionWorkflowRequest
    """Encrypted, server-managed DAG. Every step runs in its own disposable
    Run. Independent steps may run in parallel up to max_parallel_steps
    and the account's concurrent Run limit. The full JSON body must not
    exceed 4 MiB. An account can have at most 16 active managed workflows.
    """
    repeat_steps: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        workflow_id = self.workflow_id

        original = self.original.to_dict()

        repeat_steps: list[str] | Unset = UNSET
        if not isinstance(self.repeat_steps, Unset):
            repeat_steps = self.repeat_steps

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow_id": workflow_id,
                "original": original,
            }
        )
        if repeat_steps is not UNSET:
            field_dict["repeat_steps"] = repeat_steps

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_managed_execution_workflow_request import CreateManagedExecutionWorkflowRequest

        d = dict(src_dict)
        workflow_id = d.pop("workflow_id")

        original = CreateManagedExecutionWorkflowRequest.from_dict(d.pop("original"))

        repeat_steps = cast(list[str], d.pop("repeat_steps", UNSET))

        rerun_execution_pipeline_request = cls(
            workflow_id=workflow_id,
            original=original,
            repeat_steps=repeat_steps,
        )

        return rerun_execution_pipeline_request
