from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationSignalReceipt")


@_attrs_define
class OperationSignalReceipt:
    """One signal delivery. waiting reports whether the operation was waiting for this event; otherwise the run holds it
    for a later wait.

    """

    operation_id: UUID
    signal_id: str
    event: str
    duplicate: bool
    waiting: bool

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        signal_id = self.signal_id

        event = self.event

        duplicate = self.duplicate

        waiting = self.waiting

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
                "signal_id": signal_id,
                "event": event,
                "duplicate": duplicate,
                "waiting": waiting,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        signal_id = d.pop("signal_id")

        event = d.pop("event")

        duplicate = d.pop("duplicate")

        waiting = d.pop("waiting")

        operation_signal_receipt = cls(
            operation_id=operation_id,
            signal_id=signal_id,
            event=event,
            duplicate=duplicate,
            waiting=waiting,
        )

        return operation_signal_receipt
