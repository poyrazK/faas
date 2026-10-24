from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="RealtimeTicketRequest")


@_attrs_define
class RealtimeTicketRequest:
    """Realtime Ticket Request."""

    principal: str
    channels: list[str]
    ttl_seconds: int

    def to_dict(self) -> dict[str, Any]:
        principal = self.principal

        channels = self.channels

        ttl_seconds = self.ttl_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "principal": principal,
                "channels": channels,
                "ttl_seconds": ttl_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        principal = d.pop("principal")

        channels = cast(list[str], d.pop("channels"))

        ttl_seconds = d.pop("ttl_seconds")

        realtime_ticket_request = cls(
            principal=principal,
            channels=channels,
            ttl_seconds=ttl_seconds,
        )

        return realtime_ticket_request
