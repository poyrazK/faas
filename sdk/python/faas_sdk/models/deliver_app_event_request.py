from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="DeliverAppEventRequest")


@_attrs_define
class DeliverAppEventRequest:
    """Request to enqueue a signed webhook delivery owned by the source app."""

    destination: str
    """A webhook id or exact registered target URL owned by the source app."""
    type_: str
    data: Any
    """Any valid JSON value placed in the signed webhook envelope."""

    def to_dict(self) -> dict[str, Any]:
        destination = self.destination

        type_ = self.type_

        data = self.data

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "destination": destination,
                "type": type_,
                "data": data,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        destination = d.pop("destination")

        type_ = d.pop("type")

        data = d.pop("data")

        deliver_app_event_request = cls(
            destination=destination,
            type_=type_,
            data=data,
        )

        return deliver_app_event_request
