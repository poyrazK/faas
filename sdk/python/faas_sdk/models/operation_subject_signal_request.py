from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject


T = TypeVar("T", bound="OperationSubjectSignalRequest")


@_attrs_define
class OperationSubjectSignalRequest:
    """One named event for every active operation of a definition about one business subject (ADR-1197)."""

    scope: str
    operation: str
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    signal_id: str
    event: str
    payload: Any | Unset = UNSET
    """JSON each reached operation's wait step receives as its output; at most 64 KiB."""

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        operation = self.operation

        subject = self.subject.to_dict()

        signal_id = self.signal_id

        event = self.event

        payload = self.payload

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "scope": scope,
                "operation": operation,
                "subject": subject,
                "signal_id": signal_id,
                "event": event,
            }
        )
        if payload is not UNSET:
            field_dict["payload"] = payload

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject

        d = dict(src_dict)
        scope = d.pop("scope")

        operation = d.pop("operation")

        subject = OperationSubject.from_dict(d.pop("subject"))

        signal_id = d.pop("signal_id")

        event = d.pop("event")

        payload = d.pop("payload", UNSET)

        operation_subject_signal_request = cls(
            scope=scope,
            operation=operation,
            subject=subject,
            signal_id=signal_id,
            event=event,
            payload=payload,
        )

        return operation_subject_signal_request
