from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWebhookSignal")


@_attrs_define
class OperationWebhookSignal:
    """One provider event type and the operation event a waiting workflow step receives for it (ADR-20261016100929)."""

    provider_event: str
    event: str

    def to_dict(self) -> dict[str, Any]:
        provider_event = self.provider_event

        event = self.event

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "provider_event": provider_event,
                "event": event,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        provider_event = d.pop("provider_event")

        event = d.pop("event")

        operation_webhook_signal = cls(
            provider_event=provider_event,
            event=event,
        )

        return operation_webhook_signal
