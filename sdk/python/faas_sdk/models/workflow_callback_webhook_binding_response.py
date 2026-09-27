from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="WorkflowCallbackWebhookBindingResponse")


@_attrs_define
class WorkflowCallbackWebhookBindingResponse:
    """Persisted binding metadata without the endpoint's URL or secret."""

    id: UUID
    run_id: UUID
    step_name: str
    endpoint_id: UUID
    event_type: str
    object_id: str
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        run_id = str(self.run_id)

        step_name = self.step_name

        endpoint_id = str(self.endpoint_id)

        event_type = self.event_type

        object_id = self.object_id

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "run_id": run_id,
                "step_name": step_name,
                "endpoint_id": endpoint_id,
                "event_type": event_type,
                "object_id": object_id,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        run_id = UUID(d.pop("run_id"))

        step_name = d.pop("step_name")

        endpoint_id = UUID(d.pop("endpoint_id"))

        event_type = d.pop("event_type")

        object_id = d.pop("object_id")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        workflow_callback_webhook_binding_response = cls(
            id=id,
            run_id=run_id,
            step_name=step_name,
            endpoint_id=endpoint_id,
            event_type=event_type,
            object_id=object_id,
            created_at=created_at,
        )

        workflow_callback_webhook_binding_response.additional_properties = d
        return workflow_callback_webhook_binding_response

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
