from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.deliver_app_event_response_status import (
    DeliverAppEventResponseStatus,
    check_deliver_app_event_response_status,
)

T = TypeVar("T", bound="DeliverAppEventResponse")


@_attrs_define
class DeliverAppEventResponse:
    """Durable receipt for an accepted outbound webhook delivery."""

    id: str
    webhook_id: str
    destination: str
    event: str
    status: DeliverAppEventResponseStatus
    status_url: str

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        webhook_id = self.webhook_id

        destination = self.destination

        event = self.event

        status: str = self.status

        status_url = self.status_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "webhook_id": webhook_id,
                "destination": destination,
                "event": event,
                "status": status,
                "status_url": status_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        webhook_id = d.pop("webhook_id")

        destination = d.pop("destination")

        event = d.pop("event")

        status = check_deliver_app_event_response_status(d.pop("status"))

        status_url = d.pop("status_url")

        deliver_app_event_response = cls(
            id=id,
            webhook_id=webhook_id,
            destination=destination,
            event=event,
            status=status,
            status_url=status_url,
        )

        return deliver_app_event_response
