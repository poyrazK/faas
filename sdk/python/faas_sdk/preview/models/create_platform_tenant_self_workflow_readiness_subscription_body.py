from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_workflow_readiness_subscription_request_events_item import (
    OperationWorkflowReadinessSubscriptionRequestEventsItem,
    check_operation_workflow_readiness_subscription_request_events_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_digest_policy import OperationWorkflowNextActionDigestPolicy
    from ..models.operation_workflow_readiness_impact_watch_request import OperationWorkflowReadinessImpactWatchRequest


T = TypeVar("T", bound="CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody")


@_attrs_define
class CreatePlatformTenantSelfWorkflowReadinessSubscriptionBody:
    scope: str
    """OperationWorkflowReadinessSubscriptionRequest.scope. Application environment scope."""
    name: str
    """OperationWorkflowReadinessSubscriptionRequest.name. Subscription name."""
    app_id: UUID
    """OperationWorkflowReadinessSubscriptionRequest.app_id. Application UUID; required in customer requests and
    omitted in account requests."""
    next_action_digest: OperationWorkflowNextActionDigestPolicy | Unset = UNSET
    """Fixed team digest selection and cadence. Owner is both the exact action-owner filter and inbox recipient
    label, not proof of team membership. Overdue actions, deadlines within the configured approaching window and
    unaccepted handoffs qualify. Unchanged delivered items are suppressed."""
    business_impact_watches: list[OperationWorkflowReadinessImpactWatchRequest] | Unset = UNSET
    """OperationWorkflowReadinessSubscriptionRequest.business_impact_watches. Optional unique workflow/result
    watches in the subscription application, customer and environment scope. Initial successful observation is
    silent; configuration remains fixed until deletion. These targets are independent of transition discovery
    owner/operation filters."""
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
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        name = self.name

        app_id = str(self.app_id)

        next_action_digest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.next_action_digest, Unset):
            next_action_digest = self.next_action_digest.to_dict()

        business_impact_watches: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.business_impact_watches, Unset):
            business_impact_watches = []
            for business_impact_watches_item_data in self.business_impact_watches:
                business_impact_watches_item = business_impact_watches_item_data.to_dict()
                business_impact_watches.append(business_impact_watches_item)

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
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "name": name,
                "app_id": app_id,
            }
        )
        if next_action_digest is not UNSET:
            field_dict["next_action_digest"] = next_action_digest
        if business_impact_watches is not UNSET:
            field_dict["business_impact_watches"] = business_impact_watches
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
        from ..models.operation_workflow_next_action_digest_policy import OperationWorkflowNextActionDigestPolicy
        from ..models.operation_workflow_readiness_impact_watch_request import (
            OperationWorkflowReadinessImpactWatchRequest,
        )

        d = dict(src_dict)
        scope = d.pop("scope")

        name = d.pop("name")

        app_id = UUID(d.pop("app_id"))

        _next_action_digest = d.pop("next_action_digest", UNSET)
        next_action_digest: OperationWorkflowNextActionDigestPolicy | Unset
        if isinstance(_next_action_digest, Unset):
            next_action_digest = UNSET
        else:
            next_action_digest = OperationWorkflowNextActionDigestPolicy.from_dict(_next_action_digest)

        _business_impact_watches = d.pop("business_impact_watches", UNSET)
        business_impact_watches: list[OperationWorkflowReadinessImpactWatchRequest] | Unset = UNSET
        if _business_impact_watches is not UNSET:
            business_impact_watches = []
            for business_impact_watches_item_data in _business_impact_watches:
                business_impact_watches_item = OperationWorkflowReadinessImpactWatchRequest.from_dict(
                    business_impact_watches_item_data
                )

                business_impact_watches.append(business_impact_watches_item)

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

        create_platform_tenant_self_workflow_readiness_subscription_body = cls(
            scope=scope,
            name=name,
            app_id=app_id,
            next_action_digest=next_action_digest,
            business_impact_watches=business_impact_watches,
            tenant_id=tenant_id,
            workflow=workflow,
            target_operation=target_operation,
            owner=owner,
            unassigned=unassigned,
            events=events,
        )

        create_platform_tenant_self_workflow_readiness_subscription_body.additional_properties = d
        return create_platform_tenant_self_workflow_readiness_subscription_body

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
