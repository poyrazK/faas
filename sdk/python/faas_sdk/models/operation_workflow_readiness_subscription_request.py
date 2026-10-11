from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_subscription_request_events_item import (
    OperationWorkflowReadinessSubscriptionRequestEventsItem,
    check_operation_workflow_readiness_subscription_request_events_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessSubscriptionRequest")


@_attrs_define
class OperationWorkflowReadinessSubscriptionRequest:
    """Create a durable readiness inbox subscription; initial observations establish a baseline without inventing readiness
    changes.

    """

    scope: str
    """OperationWorkflowReadinessSubscriptionRequest.scope. Application environment scope."""
    name: str
    """OperationWorkflowReadinessSubscriptionRequest.name. Subscription name."""
    app_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessSubscriptionRequest.app_id. Application UUID; required in customer requests and
    omitted in account requests."""
    tenant_id: UUID | Unset = UNSET
    """OperationWorkflowReadinessSubscriptionRequest.tenant_id. Optional account-only customer selector; customer
    identity derives from authentication."""
    workflow: str | Unset = UNSET
    """OperationWorkflowReadinessSubscriptionRequest.workflow. Optional declared workflow selector."""
    target_operation: str | Unset = UNSET
    """OperationWorkflowReadinessSubscriptionRequest.target_operation. Optional transition operation selector."""
    owner: str | Unset = UNSET
    """OperationWorkflowReadinessSubscriptionRequest.owner. Optional blocker or pending verification owner
    selector."""
    unassigned: bool | Unset = UNSET
    """OperationWorkflowReadinessSubscriptionRequest.unassigned. Select ownerless blockers or verification
    obligations."""
    events: list[OperationWorkflowReadinessSubscriptionRequestEventsItem] | Unset = UNSET
    """OperationWorkflowReadinessSubscriptionRequest.events. Readiness inbox event selectors; defaults to all four
    when omitted."""

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        name = self.name

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        tenant_id: str | Unset = UNSET
        if not isinstance(self.tenant_id, Unset):
            tenant_id = str(self.tenant_id)

        workflow = self.workflow

        target_operation = self.target_operation

        owner = self.owner

        unassigned = self.unassigned

        events: list[str] | Unset = UNSET
        if not isinstance(self.events, Unset):
            events = []
            for events_item_data in self.events:
                events_item: str = events_item_data
                events.append(events_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "scope": scope,
                "name": name,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if target_operation is not UNSET:
            field_dict["target_operation"] = target_operation
        if owner is not UNSET:
            field_dict["owner"] = owner
        if unassigned is not UNSET:
            field_dict["unassigned"] = unassigned
        if events is not UNSET:
            field_dict["events"] = events

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = d.pop("scope")

        name = d.pop("name")

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

        workflow = d.pop("workflow", UNSET)

        target_operation = d.pop("target_operation", UNSET)

        owner = d.pop("owner", UNSET)

        unassigned = d.pop("unassigned", UNSET)

        _events = d.pop("events", UNSET)
        events: list[OperationWorkflowReadinessSubscriptionRequestEventsItem] | Unset = UNSET
        if _events is not UNSET:
            events = []
            for events_item_data in _events:
                events_item = check_operation_workflow_readiness_subscription_request_events_item(events_item_data)

                events.append(events_item)

        operation_workflow_readiness_subscription_request = cls(
            scope=scope,
            name=name,
            app_id=app_id,
            tenant_id=tenant_id,
            workflow=workflow,
            target_operation=target_operation,
            owner=owner,
            unassigned=unassigned,
            events=events,
        )

        return operation_workflow_readiness_subscription_request
