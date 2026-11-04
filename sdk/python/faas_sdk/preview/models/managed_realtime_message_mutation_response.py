from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedRealtimeMessageMutationResponse")


@_attrs_define
class ManagedRealtimeMessageMutationResponse:
    """Committed retained message mutation and resulting stream sequence."""

    message_id: str
    version: int
    sequence: int
    event: str
    deleted: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        message_id = self.message_id

        version = self.version

        sequence = self.sequence

        event = self.event

        deleted = self.deleted

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "message_id": message_id,
                "version": version,
                "sequence": sequence,
                "event": event,
                "deleted": deleted,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        message_id = d.pop("message_id")

        version = d.pop("version")

        sequence = d.pop("sequence")

        event = d.pop("event")

        deleted = d.pop("deleted")

        managed_realtime_message_mutation_response = cls(
            message_id=message_id,
            version=version,
            sequence=sequence,
            event=event,
            deleted=deleted,
        )

        managed_realtime_message_mutation_response.additional_properties = d
        return managed_realtime_message_mutation_response

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
