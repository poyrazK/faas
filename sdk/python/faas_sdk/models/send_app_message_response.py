from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.send_app_message_response_status import (
    SendAppMessageResponseStatus,
    check_send_app_message_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="SendAppMessageResponse")


@_attrs_define
class SendAppMessageResponse:
    """Durable receipt for a queued app-to-app message."""

    id: str
    """Durable invocation identifier."""
    event_id: str
    target_app: str
    status: SendAppMessageResponseStatus
    status_url: str
    environment: str | Unset = UNSET
    """Deployment scope captured when the message was accepted."""
    queue_binding_id: str | Unset = UNSET
    """Immutable captured queue binding identity; omitted for legacy unbound work."""
    trace_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        event_id = self.event_id

        target_app = self.target_app

        status: str = self.status

        status_url = self.status_url

        environment = self.environment

        queue_binding_id = self.queue_binding_id

        trace_id = self.trace_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "event_id": event_id,
                "target_app": target_app,
                "status": status,
                "status_url": status_url,
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

        event_id = d.pop("event_id")

        target_app = d.pop("target_app")

        status = check_send_app_message_response_status(d.pop("status"))

        status_url = d.pop("status_url")

        environment = d.pop("environment", UNSET)

        queue_binding_id = d.pop("queue_binding_id", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        send_app_message_response = cls(
            id=id,
            event_id=event_id,
            target_app=target_app,
            status=status,
            status_url=status_url,
            environment=environment,
            queue_binding_id=queue_binding_id,
            trace_id=trace_id,
        )

        return send_app_message_response
