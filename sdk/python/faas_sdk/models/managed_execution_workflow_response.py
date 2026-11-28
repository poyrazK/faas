from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_execution_workflow_response_status import (
    ManagedExecutionWorkflowResponseStatus,
    check_managed_execution_workflow_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_pipeline_response import ExecutionPipelineResponse


T = TypeVar("T", bound="ManagedExecutionWorkflowResponse")


@_attrs_define
class ManagedExecutionWorkflowResponse:
    """Server-managed workflow lifecycle metadata. Source and input are not
    returned. `next_step` is the number of steps already admitted as Runs,
    including parallel admissions.

    """

    workflow_id: str
    plan_id: str
    status: ManagedExecutionWorkflowResponseStatus
    step_count: int
    next_step: int
    created_at: datetime.datetime
    id: UUID | Unset = UNSET
    error: str | Unset = UNSET
    pipeline: ExecutionPipelineResponse | Unset = UNSET
    """Managed task pipeline state, steps and available completion details."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workflow_id = self.workflow_id

        plan_id = self.plan_id

        status: str = self.status

        step_count = self.step_count

        next_step = self.next_step

        created_at = self.created_at.isoformat()

        id: str | Unset = UNSET
        if not isinstance(self.id, Unset):
            id = str(self.id)

        error = self.error

        pipeline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.pipeline, Unset):
            pipeline = self.pipeline.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workflow_id": workflow_id,
                "plan_id": plan_id,
                "status": status,
                "step_count": step_count,
                "next_step": next_step,
                "created_at": created_at,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if error is not UNSET:
            field_dict["error"] = error
        if pipeline is not UNSET:
            field_dict["pipeline"] = pipeline

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_response import ExecutionPipelineResponse

        d = dict(src_dict)
        workflow_id = d.pop("workflow_id")

        plan_id = d.pop("plan_id")

        status = check_managed_execution_workflow_response_status(d.pop("status"))

        step_count = d.pop("step_count")

        next_step = d.pop("next_step")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _id = d.pop("id", UNSET)
        id: UUID | Unset
        if isinstance(_id, Unset):
            id = UNSET
        else:
            id = UUID(_id)

        error = d.pop("error", UNSET)

        _pipeline = d.pop("pipeline", UNSET)
        pipeline: ExecutionPipelineResponse | Unset
        if isinstance(_pipeline, Unset):
            pipeline = UNSET
        else:
            pipeline = ExecutionPipelineResponse.from_dict(_pipeline)

        managed_execution_workflow_response = cls(
            workflow_id=workflow_id,
            plan_id=plan_id,
            status=status,
            step_count=step_count,
            next_step=next_step,
            created_at=created_at,
            id=id,
            error=error,
            pipeline=pipeline,
        )

        managed_execution_workflow_response.additional_properties = d
        return managed_execution_workflow_response

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
