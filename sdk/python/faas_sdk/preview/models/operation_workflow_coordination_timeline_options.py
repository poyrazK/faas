from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowCoordinationTimelineOptions")


@_attrs_define
class OperationWorkflowCoordinationTimelineOptions:
    """OperationWorkflowCoordinationTimelineOptions. Explicit business identity and live pagination selectors."""

    app_id: UUID | Unset = UNSET
    """OperationWorkflowCoordinationTimelineOptions.app_id. Exact timeline query selector; customer identity comes
    from authentication."""
    scope: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineOptions.scope. Exact timeline query selector; customer identity comes
    from authentication."""
    tenant_id: UUID | Unset = UNSET
    """OperationWorkflowCoordinationTimelineOptions.tenant_id. Exact timeline query selector; customer identity
    comes from authentication."""
    subject_type: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineOptions.subject_type. Exact timeline query selector; customer identity
    comes from authentication."""
    subject_id: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineOptions.subject_id. Exact timeline query selector; customer identity
    comes from authentication."""
    workflow: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineOptions.workflow. Exact timeline query selector; customer identity
    comes from authentication."""
    instance_id: str | Unset = UNSET
    """OperationWorkflowCoordinationTimelineOptions.instance_id. Exact timeline query selector; customer identity
    comes from authentication."""
    offset: int | Unset = UNSET
    """OperationWorkflowCoordinationTimelineOptions.offset. Exact timeline query selector; customer identity comes
    from authentication."""
    limit: int | Unset = UNSET
    """OperationWorkflowCoordinationTimelineOptions.limit. Exact timeline query selector; customer identity comes
    from authentication."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        scope = self.scope

        tenant_id: str | Unset = UNSET
        if not isinstance(self.tenant_id, Unset):
            tenant_id = str(self.tenant_id)

        subject_type = self.subject_type

        subject_id = self.subject_id

        workflow = self.workflow

        instance_id = self.instance_id

        offset = self.offset

        limit = self.limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if scope is not UNSET:
            field_dict["scope"] = scope
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if subject_type is not UNSET:
            field_dict["subject_type"] = subject_type
        if subject_id is not UNSET:
            field_dict["subject_id"] = subject_id
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if offset is not UNSET:
            field_dict["offset"] = offset
        if limit is not UNSET:
            field_dict["limit"] = limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        scope = d.pop("scope", UNSET)

        _tenant_id = d.pop("tenant_id", UNSET)
        tenant_id: UUID | Unset
        if isinstance(_tenant_id, Unset):
            tenant_id = UNSET
        else:
            tenant_id = UUID(_tenant_id)

        subject_type = d.pop("subject_type", UNSET)

        subject_id = d.pop("subject_id", UNSET)

        workflow = d.pop("workflow", UNSET)

        instance_id = d.pop("instance_id", UNSET)

        offset = d.pop("offset", UNSET)

        limit = d.pop("limit", UNSET)

        operation_workflow_coordination_timeline_options = cls(
            app_id=app_id,
            scope=scope,
            tenant_id=tenant_id,
            subject_type=subject_type,
            subject_id=subject_id,
            workflow=workflow,
            instance_id=instance_id,
            offset=offset,
            limit=limit,
        )

        operation_workflow_coordination_timeline_options.additional_properties = d
        return operation_workflow_coordination_timeline_options

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
