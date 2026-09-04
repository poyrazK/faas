from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.inject_workflow_event_response_status import (
    InjectWorkflowEventResponseStatus,
    check_inject_workflow_event_response_status,
)

T = TypeVar("T", bound="InjectWorkflowEventResponse")


@_attrs_define
class InjectWorkflowEventResponse:
    """Acknowledgement that an external workflow event was recorded."""

    status: InjectWorkflowEventResponseStatus
    event_name: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        event_name = self.event_name

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "event_name": event_name,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_inject_workflow_event_response_status(d.pop("status"))

        event_name = d.pop("event_name")

        inject_workflow_event_response = cls(
            status=status,
            event_name=event_name,
        )

        inject_workflow_event_response.additional_properties = d
        return inject_workflow_event_response

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
