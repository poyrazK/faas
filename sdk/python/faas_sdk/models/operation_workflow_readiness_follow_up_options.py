from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_follow_up_options_due import (
    OperationWorkflowReadinessFollowUpOptionsDue,
    check_operation_workflow_readiness_follow_up_options_due,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessFollowUpOptions")


@_attrs_define
class OperationWorkflowReadinessFollowUpOptions:
    """Selectors for the live retained follow-up queue; offset pages may move as coordination is edited."""

    scope: str
    """OperationWorkflowReadinessFollowUpOptions.scope. Follow-up queue selector; customer tenant derives from
    authentication."""
    app_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.app_id. Follow-up queue selector; customer tenant derives from
    authentication."""
    tenant_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.tenant_id. Follow-up queue selector; customer tenant derives from
    authentication."""
    owner: str | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.owner. Follow-up queue selector; customer tenant derives from
    authentication."""
    workflow: str | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.workflow. Follow-up queue selector; customer tenant derives from
    authentication."""
    due: OperationWorkflowReadinessFollowUpOptionsDue | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.due. Follow-up queue selector; customer tenant derives from
    authentication."""
    offset: int | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.offset. Follow-up queue selector; customer tenant derives from
    authentication."""
    limit: int | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.limit. Follow-up queue selector; customer tenant derives from
    authentication."""
    reminder_only: bool | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.reminder_only. Select only active published due reminders;
    defaults to false."""

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        tenant_id: str | Unset = UNSET
        if not isinstance(self.tenant_id, Unset):
            tenant_id = str(self.tenant_id)

        owner = self.owner

        workflow = self.workflow

        due: str | Unset = UNSET
        if not isinstance(self.due, Unset):
            due = self.due

        offset = self.offset

        limit = self.limit

        reminder_only = self.reminder_only

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "scope": scope,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if owner is not UNSET:
            field_dict["owner"] = owner
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if due is not UNSET:
            field_dict["due"] = due
        if offset is not UNSET:
            field_dict["offset"] = offset
        if limit is not UNSET:
            field_dict["limit"] = limit
        if reminder_only is not UNSET:
            field_dict["reminder_only"] = reminder_only

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = d.pop("scope")

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        _tenant_id = d.pop("tenant_id", UNSET)
        tenant_id: UUID | Unset
        if isinstance(_tenant_id, Unset):
            tenant_id = UNSET
        else:
            tenant_id = UUID(_tenant_id)

        owner = d.pop("owner", UNSET)

        workflow = d.pop("workflow", UNSET)

        _due = d.pop("due", UNSET)
        due: OperationWorkflowReadinessFollowUpOptionsDue | Unset
        if isinstance(_due, Unset):
            due = UNSET
        else:
            due = check_operation_workflow_readiness_follow_up_options_due(_due)

        offset = d.pop("offset", UNSET)

        limit = d.pop("limit", UNSET)

        reminder_only = d.pop("reminder_only", UNSET)

        operation_workflow_readiness_follow_up_options = cls(
            scope=scope,
            app_id=app_id,
            tenant_id=tenant_id,
            owner=owner,
            workflow=workflow,
            due=due,
            offset=offset,
            limit=limit,
            reminder_only=reminder_only,
        )

        return operation_workflow_readiness_follow_up_options
