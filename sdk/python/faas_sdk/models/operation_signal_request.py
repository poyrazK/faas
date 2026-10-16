from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationSignalRequest")


@_attrs_define
class OperationSignalRequest:
    """One named event for an operation (ADR-1197)."""

    signal_id: str
    """Idempotency key for this delivery"""
    event: str
    payload: Any | Unset = UNSET
    """JSON delivered as the wait step's output; at most 64 KiB."""

    def to_dict(self) -> dict[str, Any]:
        signal_id = self.signal_id

        event = self.event

        payload = self.payload

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "signal_id": signal_id,
                "event": event,
            }
        )
        if payload is not UNSET:
            field_dict["payload"] = payload

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        signal_id = d.pop("signal_id")

        event = d.pop("event")

        payload = d.pop("payload", UNSET)

        operation_signal_request = cls(
            signal_id=signal_id,
            event=event,
            payload=payload,
        )

        return operation_signal_request
