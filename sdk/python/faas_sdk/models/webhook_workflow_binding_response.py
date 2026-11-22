from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.webhook_workflow_binding_response_filter import WebhookWorkflowBindingResponseFilter


T = TypeVar("T", bound="WebhookWorkflowBindingResponse")


@_attrs_define
class WebhookWorkflowBindingResponse:
    """Captured stored workflow name, event selection and current endpoint binding revision."""

    endpoint_id: UUID
    workflow_name: str
    event_type: str
    filter_: WebhookWorkflowBindingResponseFilter
    version: int
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        endpoint_id = str(self.endpoint_id)

        workflow_name = self.workflow_name

        event_type = self.event_type

        filter_ = self.filter_.to_dict()

        version = self.version

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "endpoint_id": endpoint_id,
                "workflow_name": workflow_name,
                "event_type": event_type,
                "filter": filter_,
                "version": version,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.webhook_workflow_binding_response_filter import WebhookWorkflowBindingResponseFilter

        d = dict(src_dict)
        endpoint_id = UUID(d.pop("endpoint_id"))

        workflow_name = d.pop("workflow_name")

        event_type = d.pop("event_type")

        filter_ = WebhookWorkflowBindingResponseFilter.from_dict(d.pop("filter"))

        version = d.pop("version")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        webhook_workflow_binding_response = cls(
            endpoint_id=endpoint_id,
            workflow_name=workflow_name,
            event_type=event_type,
            filter_=filter_,
            version=version,
            updated_at=updated_at,
        )

        webhook_workflow_binding_response.additional_properties = d
        return webhook_workflow_binding_response

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
