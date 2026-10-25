from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CreateWorkflowCallbackWebhookBindingRequest")


@_attrs_define
class CreateWorkflowCallbackWebhookBindingRequest:
    """Exact Stripe event/object correlation for one callback on an existing signed inbound endpoint."""

    endpoint_id: UUID
    event_type: str
    object_id: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        endpoint_id = str(self.endpoint_id)

        event_type = self.event_type

        object_id = self.object_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "endpoint_id": endpoint_id,
                "event_type": event_type,
                "object_id": object_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        endpoint_id = UUID(d.pop("endpoint_id"))

        event_type = d.pop("event_type")

        object_id = d.pop("object_id")

        create_workflow_callback_webhook_binding_request = cls(
            endpoint_id=endpoint_id,
            event_type=event_type,
            object_id=object_id,
        )

        create_workflow_callback_webhook_binding_request.additional_properties = d
        return create_workflow_callback_webhook_binding_request

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
