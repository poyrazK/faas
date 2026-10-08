from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowStateReport")


@_attrs_define
class OperationWorkflowStateReport:
    """Idempotent app-reported state update already committed with the business write. Revision is assigned transactionally
    by the application SDK.

    """

    id: UUID
    workflow: str
    instance_id: str
    state: str
    revision: int
    occurred_at: datetime.datetime
    from_state: str | Unset = UNSET
    """Current app database state before the requested declared transition. Required when the pinned workflow
    declares transitions."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        revision = self.revision

        occurred_at = self.occurred_at.isoformat()

        from_state = self.from_state

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "workflow": workflow,
                "instance_id": instance_id,
                "state": state,
                "revision": revision,
                "occurred_at": occurred_at,
            }
        )
        if from_state is not UNSET:
            field_dict["from_state"] = from_state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        state = d.pop("state")

        revision = d.pop("revision")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        from_state = d.pop("from_state", UNSET)

        operation_workflow_state_report = cls(
            id=id,
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            revision=revision,
            occurred_at=occurred_at,
            from_state=from_state,
        )

        return operation_workflow_state_report
