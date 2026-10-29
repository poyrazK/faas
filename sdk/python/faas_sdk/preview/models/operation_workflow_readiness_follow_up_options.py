from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_follow_up_options_action_trigger import (
    OperationWorkflowReadinessFollowUpOptionsActionTrigger,
    check_operation_workflow_readiness_follow_up_options_action_trigger,
)
from ..models.operation_workflow_readiness_follow_up_options_due import (
    OperationWorkflowReadinessFollowUpOptionsDue,
    check_operation_workflow_readiness_follow_up_options_due,
)
from ..models.operation_workflow_readiness_follow_up_options_impact_kind import (
    OperationWorkflowReadinessFollowUpOptionsImpactKind,
    check_operation_workflow_readiness_follow_up_options_impact_kind,
)
from ..models.operation_workflow_readiness_follow_up_options_impact_priority import (
    OperationWorkflowReadinessFollowUpOptionsImpactPriority,
    check_operation_workflow_readiness_follow_up_options_impact_priority,
)
from ..models.operation_workflow_readiness_follow_up_options_sort import (
    OperationWorkflowReadinessFollowUpOptionsSort,
    check_operation_workflow_readiness_follow_up_options_sort,
)
from ..models.operation_workflow_readiness_follow_up_options_view import (
    OperationWorkflowReadinessFollowUpOptionsView,
    check_operation_workflow_readiness_follow_up_options_view,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessFollowUpOptions")


@_attrs_define
class OperationWorkflowReadinessFollowUpOptions:
    """Selectors for the live retained follow-up queue; offset pages may move as coordination is edited."""

    scope: str
    """OperationWorkflowReadinessFollowUpOptions.scope. Follow-up queue selector; customer tenant derives from
    authentication."""
    view: OperationWorkflowReadinessFollowUpOptionsView | Unset = UNSET
    """Default follow-up view or cross-case next-action rows. Next-action view uses action owners and deadlines and
    rejects reminder/business-impact selectors."""
    action_kind: str | Unset = UNSET
    """Exact next-action kind; requires view=next_actions."""
    action_trigger: OperationWorkflowReadinessFollowUpOptionsActionTrigger | Unset = UNSET
    """Action trigger selector; requires view=next_actions."""
    impact_priority: OperationWorkflowReadinessFollowUpOptionsImpactPriority | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.impact_priority. Exact unresolved review priority filter."""
    impact_kind: OperationWorkflowReadinessFollowUpOptionsImpactKind | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.impact_kind. Exact protected business result category."""
    impact_code: str | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.impact_code. Exact protected result code, requires impact_kind."""
    sort: OperationWorkflowReadinessFollowUpOptionsSort | Unset = UNSET
    """OperationWorkflowReadinessFollowUpOptions.sort. Due by default; business_impact orders overdue first, then
    declared unresolved review priority and due time before pagination."""
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

        view: str | Unset = UNSET
        if not isinstance(self.view, Unset):
            view = self.view

        action_kind = self.action_kind

        action_trigger: str | Unset = UNSET
        if not isinstance(self.action_trigger, Unset):
            action_trigger = self.action_trigger

        impact_priority: str | Unset = UNSET
        if not isinstance(self.impact_priority, Unset):
            impact_priority = self.impact_priority

        impact_kind: str | Unset = UNSET
        if not isinstance(self.impact_kind, Unset):
            impact_kind = self.impact_kind

        impact_code = self.impact_code

        sort: str | Unset = UNSET
        if not isinstance(self.sort, Unset):
            sort = self.sort

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
        if view is not UNSET:
            field_dict["view"] = view
        if action_kind is not UNSET:
            field_dict["action_kind"] = action_kind
        if action_trigger is not UNSET:
            field_dict["action_trigger"] = action_trigger
        if impact_priority is not UNSET:
            field_dict["impact_priority"] = impact_priority
        if impact_kind is not UNSET:
            field_dict["impact_kind"] = impact_kind
        if impact_code is not UNSET:
            field_dict["impact_code"] = impact_code
        if sort is not UNSET:
            field_dict["sort"] = sort
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

        _view = d.pop("view", UNSET)
        view: OperationWorkflowReadinessFollowUpOptionsView | Unset
        if isinstance(_view, Unset):
            view = UNSET
        else:
            view = check_operation_workflow_readiness_follow_up_options_view(_view)

        action_kind = d.pop("action_kind", UNSET)

        _action_trigger = d.pop("action_trigger", UNSET)
        action_trigger: OperationWorkflowReadinessFollowUpOptionsActionTrigger | Unset
        if isinstance(_action_trigger, Unset):
            action_trigger = UNSET
        else:
            action_trigger = check_operation_workflow_readiness_follow_up_options_action_trigger(_action_trigger)

        _impact_priority = d.pop("impact_priority", UNSET)
        impact_priority: OperationWorkflowReadinessFollowUpOptionsImpactPriority | Unset
        if isinstance(_impact_priority, Unset):
            impact_priority = UNSET
        else:
            impact_priority = check_operation_workflow_readiness_follow_up_options_impact_priority(_impact_priority)

        _impact_kind = d.pop("impact_kind", UNSET)
        impact_kind: OperationWorkflowReadinessFollowUpOptionsImpactKind | Unset
        if isinstance(_impact_kind, Unset):
            impact_kind = UNSET
        else:
            impact_kind = check_operation_workflow_readiness_follow_up_options_impact_kind(_impact_kind)

        impact_code = d.pop("impact_code", UNSET)

        _sort = d.pop("sort", UNSET)
        sort: OperationWorkflowReadinessFollowUpOptionsSort | Unset
        if isinstance(_sort, Unset):
            sort = UNSET
        else:
            sort = check_operation_workflow_readiness_follow_up_options_sort(_sort)

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
            view=view,
            action_kind=action_kind,
            action_trigger=action_trigger,
            impact_priority=impact_priority,
            impact_kind=impact_kind,
            impact_code=impact_code,
            sort=sort,
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
