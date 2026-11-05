from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimePublisherPublishResponse")


@_attrs_define
class RealtimePublisherPublishResponse:
    """Realtime Publisher Publish Response."""

    sequence: str
    durable: bool
    replayed: bool
    partial: bool
    """Immediate live fanout may be incomplete; retained delivery is committed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        durable = self.durable

        replayed = self.replayed

        partial = self.partial

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "durable": durable,
                "replayed": replayed,
                "partial": partial,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        durable = d.pop("durable")

        replayed = d.pop("replayed")

        partial = d.pop("partial")

        realtime_publisher_publish_response = cls(
            sequence=sequence,
            durable=durable,
            replayed=replayed,
            partial=partial,
        )

        realtime_publisher_publish_response.additional_properties = d
        return realtime_publisher_publish_response

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
