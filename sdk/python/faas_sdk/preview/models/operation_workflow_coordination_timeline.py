from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_coordination_timeline_event import OperationWorkflowCoordinationTimelineEvent
    from ..models.operation_workflow_coordination_timeline_source import OperationWorkflowCoordinationTimelineSource


T = TypeVar("T", bound="OperationWorkflowCoordinationTimeline")


@_attrs_define
class OperationWorkflowCoordinationTimeline:
    """OperationWorkflowCoordinationTimeline. Retained inbox coordination projection metadata."""

    items: list[OperationWorkflowCoordinationTimelineEvent]
    """OperationWorkflowCoordinationTimeline.items. Chronological page ordered by retained server clock then stable
    event identity."""
    sources: list[OperationWorkflowCoordinationTimelineSource]
    """OperationWorkflowCoordinationTimeline.sources. Coverage of current subscriptions within the authenticated
    role, app and environment."""
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    """OperationWorkflowCoordinationTimeline.workflow. Exact requested workflow name."""
    instance_id: str
    """OperationWorkflowCoordinationTimeline.instance_id. Exact requested workflow instance."""
    scope: str
    """OperationWorkflowCoordinationTimeline.scope. Application environment."""
    evaluated_at: datetime.datetime
    """OperationWorkflowCoordinationTimeline.evaluated_at. Server clock after independent source reads."""
    total_count: int
    """OperationWorkflowCoordinationTimeline.total_count. Matching retained event count before live pagination."""
    next_offset: int
    """OperationWorkflowCoordinationTimeline.next_offset. Offset for the next live page."""
    has_more: bool
    """OperationWorkflowCoordinationTimeline.has_more. More events existed at evaluation."""
    retention_limited: bool
    """OperationWorkflowCoordinationTimeline.retention_limited. Always true: deleted sources and pruned records
    cannot be reconstructed."""
    sources_truncated: bool
    """OperationWorkflowCoordinationTimeline.sources_truncated. Current source list exceeded the bounded
    projection."""
    history_truncated: bool
    """OperationWorkflowCoordinationTimeline.history_truncated. A matching response target lost older assignment
    history."""
    limitations: list[str]
    """OperationWorkflowCoordinationTimeline.limitations. Explicit retention, snapshot and pagination limitations."""
    platform_tenant_id: UUID | Unset = UNSET
    """OperationWorkflowCoordinationTimeline.platform_tenant_id. Operator-only exact customer selection; omitted
    for account-owned workflows."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        sources = []
        for sources_item_data in self.sources:
            sources_item = sources_item_data.to_dict()
            sources.append(sources_item)

        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        scope = self.scope

        evaluated_at = self.evaluated_at.isoformat()

        total_count = self.total_count

        next_offset = self.next_offset

        has_more = self.has_more

        retention_limited = self.retention_limited

        sources_truncated = self.sources_truncated

        history_truncated = self.history_truncated

        limitations = self.limitations

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "sources": sources,
                "subject": subject,
                "workflow": workflow,
                "instance_id": instance_id,
                "scope": scope,
                "evaluated_at": evaluated_at,
                "total_count": total_count,
                "next_offset": next_offset,
                "has_more": has_more,
                "retention_limited": retention_limited,
                "sources_truncated": sources_truncated,
                "history_truncated": history_truncated,
                "limitations": limitations,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_coordination_timeline_event import OperationWorkflowCoordinationTimelineEvent
        from ..models.operation_workflow_coordination_timeline_source import OperationWorkflowCoordinationTimelineSource

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowCoordinationTimelineEvent.from_dict(items_item_data)

            items.append(items_item)

        sources = []
        _sources = d.pop("sources")
        for sources_item_data in _sources:
            sources_item = OperationWorkflowCoordinationTimelineSource.from_dict(sources_item_data)

            sources.append(sources_item)

        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        scope = d.pop("scope")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        total_count = d.pop("total_count")

        next_offset = d.pop("next_offset")

        has_more = d.pop("has_more")

        retention_limited = d.pop("retention_limited")

        sources_truncated = d.pop("sources_truncated")

        history_truncated = d.pop("history_truncated")

        limitations = cast(list[str], d.pop("limitations"))

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_coordination_timeline = cls(
            items=items,
            sources=sources,
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            scope=scope,
            evaluated_at=evaluated_at,
            total_count=total_count,
            next_offset=next_offset,
            has_more=has_more,
            retention_limited=retention_limited,
            sources_truncated=sources_truncated,
            history_truncated=history_truncated,
            limitations=limitations,
            platform_tenant_id=platform_tenant_id,
        )

        operation_workflow_coordination_timeline.additional_properties = d
        return operation_workflow_coordination_timeline

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
