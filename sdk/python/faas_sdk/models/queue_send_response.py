from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="QueueSendResponse")


@_attrs_define
class QueueSendResponse:
    """201 — body of a freshly-enqueued queue row."""

    id: str
    environment: str | Unset = UNSET
    """Environment recorded for the newly accepted queue message."""
    queue_binding_id: str | Unset = UNSET
    """Binding identity captured for this accepted message; absent for unbound legacy sends."""
    trace_id: str | Unset = UNSET
    """Canonical platform trace id when the request carried a valid trace context."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        environment = self.environment

        queue_binding_id = self.queue_binding_id

        trace_id = self.trace_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment
        if queue_binding_id is not UNSET:
            field_dict["queue_binding_id"] = queue_binding_id
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        environment = d.pop("environment", UNSET)

        queue_binding_id = d.pop("queue_binding_id", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        queue_send_response = cls(
            id=id,
            environment=environment,
            queue_binding_id=queue_binding_id,
            trace_id=trace_id,
        )

        queue_send_response.additional_properties = d
        return queue_send_response

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
