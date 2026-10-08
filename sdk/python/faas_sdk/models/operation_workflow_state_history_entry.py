from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowStateHistoryEntry")


@_attrs_define
class OperationWorkflowStateHistoryEntry:
    """One retained app-reported state update. Pages are ordered by revision, then stable publication and report
    identifiers.

    """

    id: UUID
    operation_id: UUID
    workflow: str
    instance_id: str
    state: str
    revision: int
    occurred_at: datetime.datetime
    published_at: datetime.datetime
    from_state: str | Unset = UNSET
    """App state immediately before this retained revision"""
    platform_tenant_id: UUID | Unset = UNSET
    """Account-owner tenant identifier attached to this report in operator feeds."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        operation_id = str(self.operation_id)

        workflow = self.workflow

        instance_id = self.instance_id

        state = self.state

        revision = self.revision

        occurred_at = self.occurred_at.isoformat()

        published_at = self.published_at.isoformat()

        from_state = self.from_state

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "operation_id": operation_id,
                "workflow": workflow,
                "instance_id": instance_id,
                "state": state,
                "revision": revision,
                "occurred_at": occurred_at,
                "published_at": published_at,
            }
        )
        if from_state is not UNSET:
            field_dict["from_state"] = from_state
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

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

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        published_at = datetime.datetime.fromisoformat(d.pop("published_at"))

        from_state = d.pop("from_state", UNSET)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_state_history_entry = cls(
            id=id,
            operation_id=operation_id,
            workflow=workflow,
            instance_id=instance_id,
            state=state,
            revision=revision,
            occurred_at=occurred_at,
            published_at=published_at,
            from_state=from_state,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_state_history_entry
