from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowStateReportResponse")


@_attrs_define
class OperationWorkflowStateReportResponse:
    id: UUID
    operation_id: UUID
    workflow: str
    instance_id: str
    state: str
    revision: int
    from_state: str | Unset = UNSET
    """Previous app state when a declared transition was reported."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        operation_id = str(self.operation_id)

        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        revision = self.revision

        from_state = self.from_state

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "operation_id": operation_id,
                "workflow": workflow,
                "instance_id": instance_id,
                "state": state,
                "revision": revision,
            }
        )
        if from_state is not UNSET:
            field_dict["from_state"] = from_state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        operation_id = UUID(d.pop("operation_id"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        revision = d.pop("revision")

        from_state = d.pop("from_state", UNSET)

        operation_workflow_state_report_response = cls(
            id=id,
            operation_id=operation_id,
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            revision=revision,
            from_state=from_state,
        )

        return operation_workflow_state_report_response
